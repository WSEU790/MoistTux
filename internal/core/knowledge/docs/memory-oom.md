# Memory, swap and the OOM killer
> Reading memory use correctly, recognising OOM kills, leaks and swap pressure, and what to do about them.
Tags: memory, ram, oom, out of memory, swap, swappiness, leak, cache, available, cgroup, MemoryMax

## Reading memory use

`free -h` shows **used**, **buff/cache** and **available**. Linux uses free RAM for file cache; that cache is released when programs need memory. The number that matters is **available**: memory obtainable without swapping. Low "free" with high "available" is healthy.

Pressure signs: `available` below ~10% of total, swap usage growing, `si`/`so` (swap in/out) non-zero in `vmstat 1 5`, high iowait, the OOM killer in the kernel log.

In NEXUS: `get_memory_metrics`, `list_processes` (sort by memory), catalog `free`, `ps_top_mem`, `vmstat`, `dmesg_errors`, `swap_show`.

## The OOM killer

When memory runs out, the kernel kills a process to survive. Evidence:

```
dmesg -T | grep -iE 'out of memory|oom-kill|killed process'
journalctl -k -g 'Out of memory'
```

A service killed by the OOM killer shows `code=killed, signal=KILL` in `systemctl status`. systemd's own `systemd-oomd` (Ubuntu desktop and some servers) may also kill whole cgroups under pressure: `journalctl -u systemd-oomd`.

Response:

1. Identify what used the memory at the time (the OOM report lists processes with their RSS).
2. Decide whether it is a leak (memory grows steadily until killed), a spike (batch job, traffic burst), or simply too little RAM for the workload.
3. Short term: restart the leaking service, limit concurrency (worker counts, PHP-FPM `pm.max_children`, database connection pools), add swap as a buffer.
4. Long term: fix the leak, size the server for the workload, or cap the service with `MemoryMax=` so it cannot take the whole machine down.

## Finding the consumer

* `ps -eo pid,user,rss,%mem,args --sort=-rss | head` — resident memory per process (RSS counts shared pages for every process, so totals overstate).
* Per service: `systemctl status UNIT` shows the cgroup's memory; `systemd-cgtop -m` ranks cgroups.
* Databases and JVMs reserve memory by configuration (`innodb_buffer_pool_size`, `shared_buffers`, `-Xmx`) — their size is intentional; check settings against total RAM.
* Containers: `docker stats --no-stream`.

## Memory leaks

A leak shows as a steady rise of one process's RSS over hours or days without returning, often ending in an OOM kill and a restart, then repeating. Confirm with NEXUS's metrics history. Mitigate with a scheduled restart only as a stopgap; report the leak to whoever maintains the software, with version and graph.

## Swap

Swap gives breathing room and lets the kernel move idle pages out of RAM. Heavy, continuous swapping (thrashing) makes everything slow — more swap does not fix that; less memory use or more RAM does.

Add a swap file (2–4 GB is typical for servers):

```
fallocate -l 2G /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab
```

Check with `swapon --show`. `vm.swappiness` (default 60) controls eagerness to swap; 10–30 is common for servers with enough RAM: `sysctl vm.swappiness=20` and persist it in `/etc/sysctl.d/99-swappiness.conf`.

## Limiting a service

```
systemctl edit myservice
[Service]
MemoryHigh=1.5G
MemoryMax=2G
```

`MemoryHigh` throttles and reclaims; `MemoryMax` is the hard limit (OOM inside the service's cgroup only). Then `systemctl daemon-reload` and restart the service.

## Caches and "drop_caches"

`echo 3 > /proc/sys/vm/drop_caches` empties the page cache. It does not fix memory problems — the cache was reclaimable anyway — and causes a burst of disk reads afterwards. Do not use it as a fix.
