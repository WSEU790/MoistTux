# CPU, load and performance troubleshooting
> A method for slow servers: load average, CPU time categories, iowait, steal, runaway processes, and the USE method.
Tags: cpu, load, load average, performance, slow, iowait, steal, top, vmstat, use method, nice, runaway process

## A method: USE

For each resource (CPU, memory, disk, network) check **U**tilisation, **S**aturation and **E**rrors. Work from evidence to cause; do not restart things until you know what is wrong.

1. When did it start, and what changed (deploys, updates, traffic, cron jobs)?
2. CPU: utilisation per category, run-queue length.
3. Memory: available, swapping, OOM kills.
4. Disk: utilisation, latency, iowait, full filesystems.
5. Network: errors, drops, connection counts, saturation.
6. Application: error logs, slow queries, worker pool exhaustion.

In NEXUS: `get_system_metrics`, `get_cpu_metrics`, `get_metrics_history`, `list_processes`, `inspect_process`, catalog `uptime`, `vmstat`, `ps_top_cpu`, `lscpu`, `kill_term`.

## Load average

`uptime` shows 1-, 5- and 15-minute load averages: the number of tasks running or waiting for CPU **or uninterruptible I/O**. Compare with the number of CPU cores (`nproc`): load 8 on 8 cores is full; load 8 on 2 cores is overloaded. High load with low CPU use usually means processes are stuck waiting for disk or NFS (state `D` in `ps`).

## CPU time categories

From `top`, `vmstat` or NEXUS metrics:

* **us/user** — application code. High: a busy or runaway process.
* **sy/system** — kernel work: lots of syscalls, network packets, context switches.
* **wa/iowait** — CPU idle while waiting for disk. High: storage is the bottleneck.
* **st/steal** — time the hypervisor gave your vCPU to someone else. Sustained steal above ~10% means the host is oversubscribed; contact the provider or resize.
* **si/softirq** — network packet processing at high rates.

## A process using 100% CPU

1. Identify it: `ps -eo pid,user,%cpu,etime,args --sort=-%cpu | head`.
2. Is it expected (backup, compression, build, scheduled job) and temporary? Let it finish, or lower its priority: `renice +10 -p PID`.
3. Is it a service? Read its logs; check for loops, retries, a bad request pattern, or a recent change. Restart the service (not `kill -9`) if it is stuck.
4. Unknown process, unexpected user, odd name or path (`/tmp`, `/dev/shm`)? Treat it as a possible compromise — see the security incident runbook before killing it.

## High iowait

* Find the busy disk and processes: `iostat -xz 1` (package sysstat), `iotop -o`.
* Common causes: database without enough memory for its working set, swapping, backups or log compression running in the day, a failing disk retrying reads (check `dmesg`), a full disk causing retries.
* Fixes depend on the cause: schedule heavy jobs at night, add RAM, move to faster storage, tune the database.

## Many processes or threads

Check `ps -e --no-headers | wc -l` and `ps -eLf | wc -l`. Thousands of short-lived processes (fork storms from cron or CGI), zombie processes (state `Z`, a parent not reaping children), or connection-per-process servers under load. Zombies use no CPU; fix or restart the parent.

## Context: virtual machines and bursting

Cloud "burstable" instances (credits) slow down sharply when credits run out — CPU appears capped at a percentage. Check the provider's console for CPU credits and steal time.

## Verify after a fix

Compare the same metrics as before: load relative to cores, per-category CPU, response times of the service. Watch for an hour; a problem that returns on a schedule points to cron or timers.
