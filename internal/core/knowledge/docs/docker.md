# Docker and Docker Compose
> Container health, logs, restarts, disk clean-up, networking and Compose projects.
Tags: docker, containers, compose, docker-compose, images, volumes, prune, restart policy, healthcheck, logs

## Inspecting containers

* `docker ps -a` — all containers with status (`Up`, `Exited (1)`, `Restarting`, `(unhealthy)`).
* `docker logs --tail 200 NAME` (add `-t` for timestamps, `--since 30m`).
* `docker inspect NAME` — configuration, restart policy, mounts, networks, health check results (`.State.Health`).
* `docker stats --no-stream` — CPU and memory per container.

In NEXUS: `list_docker_containers`, `inspect_container`, `get_container_logs`, `restart_container`, `start_container`, `stop_container`, catalog `docker_ps_all`, `docker_df`, `docker_prune`, `docker_image_prune_all`, `docker_pull`.

## Exit codes

* `0` — finished normally (one-off job, or the main process ended).
* `1` / other — application error: read the logs.
* `137` — killed with SIGKILL: often the OOM killer (`docker inspect` shows `OOMKilled: true`) or `docker stop` timing out.
* `139` — segmentation fault. `143` — SIGTERM (normal stop).

## Restart loops

`Restarting (1) 5 seconds ago` with a rising restart count: the main process keeps failing. Read the first error after a start in `docker logs`. Typical causes: missing environment variables, a database not reachable (`depends_on` does not wait for readiness unless a healthcheck condition is used), wrong volume permissions, a port conflict.

## Unhealthy containers

The container runs but its `HEALTHCHECK` fails. Look at `docker inspect --format '{{json .State.Health}}' NAME` for the probe output. The application may be overloaded, waiting on a dependency, or the health check itself may be wrong.

## Compose

* Projects are directories with `compose.yaml` / `docker-compose.yml`. Run commands from that directory or with `-f`.
* `docker compose ps`, `docker compose logs -f SERVICE`, `docker compose up -d` (applies changes), `docker compose pull && docker compose up -d` (update images), `docker compose down` (removes containers and networks, keeps named volumes; `down -v` deletes volumes — data loss).
* Find a container's project: label `com.docker.compose.project` and `com.docker.compose.project.working_dir` in `docker inspect`.

## Disk clean-up

`docker system df` shows images, containers, volumes and build cache.

* `docker system prune -f` — stopped containers, unused networks, dangling images, build cache. Safe for running workloads.
* `docker image prune -af` — every image not used by a container; they are pulled again when needed.
* Volumes hold data: never run `docker volume prune` or `system prune --volumes` without confirming each volume is unused.
* Cap log growth with `max-size`/`max-file` in `/etc/docker/daemon.json` (see the disk runbook).

## Networking and the firewall

Published ports (`-p 8080:80`) bypass ufw (see the firewall runbook). Bind internal services to localhost (`127.0.0.1:8080:80`) and expose them through a reverse proxy. Containers on the same user-defined network reach each other by service name.

## Updates

Pull the new image, recreate the container (`docker compose up -d`), check logs and health, and keep the previous image tag available to roll back. Pin image versions (`postgres:16.4`) instead of `latest` for anything stateful.

## Access

Membership of the `docker` group is equivalent to root on the host. Grant it deliberately (NEXUS's agent gets it only when installed with `--docker`).
