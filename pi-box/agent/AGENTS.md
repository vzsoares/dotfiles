# pi-box

You run inside an isolated container. Nothing here needs approval.

## Where you are

- Your working directory is a task folder, `~/pi-box/work/<task-id>/` on the
  host, mounted at the same path. It is the only writable host folder.
- `~/pi-box/in/` (same path) holds read-only inputs, when there are any.
- No repositories, credentials, git remotes, docker socket or host services
  are reachable. Private networks and the host gateway are blocked; the public
  internet is open.

## Output contract

- Put every deliverable in `out/` inside the task folder.
- Write `RESULT.md` in the task folder: a summary, the sources you used, and
  anything left undone.
- End your final message with `WORKER_DONE <task-id>`.
