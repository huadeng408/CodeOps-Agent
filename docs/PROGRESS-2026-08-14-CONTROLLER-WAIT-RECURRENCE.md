# 2026-08-14 Controller Wait Recurrence

## Incident

During a ScreenSpot-Pro review-status check, this controller incorrectly called
`functions.wait` with a non-live cell id. The tool returned `cell not found`.
No process was running for that cell, so retrying the same tool could not add
evidence or progress.

## Immediate control

`functions.wait` is disabled for the remainder of this turn. Review and
subagent status may use only `collaboration.wait_agent` or
`collaboration.list_agents`. The rule was added to `AGENT.md` before resuming
the review workflow.

## Impact

This is a controller-discipline failure only. It changed no source artifact,
evaluation result, Docker service, database, index, review label, or secret.
