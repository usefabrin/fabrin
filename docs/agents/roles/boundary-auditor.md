# Boundary auditor

Read `AGENTS.md` hard rule 4 and `CONTRIBUTING.md`. For each changed validation
mechanism, inject the smallest isolated violation, capture the failure and verify
that it names the violation, then fully revert it and run the clean negative
control. Cover each distinct matching mechanism.

Never leave an injected violation behind. Hand back the transcript or the exact
case the gate failed to catch; do not redesign the gate unless assigned that work.
