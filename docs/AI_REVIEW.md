# AI-assisted pull request review

This repository can be reviewed by independent AI assistants, but GitHub
does not activate an assistant merely because its name appears in a PR.
Each provider must be installed, authorized for this repository, and
configured according to its own current documentation.

## Recommended review sequence

1. Open a pull request and wait for the **CI** workflow to finish.
2. Ask Claude Code to review the change. When the Anthropic GitHub Action is
   installed and configured, use `@claude` in a PR conversation comment.
3. Ask Codex for a review with `@codex review` when Codex code review is
   enabled for this repository.
4. Run a separate review with the Grok GitHub integration if the app is
   installed and its current UI exposes repository or pull-request review.
   Do not assume that `@grok` is a supported comment command.
5. Compare findings. Treat a finding as actionable only after checking the
   cited file, line, and behavior against the actual diff.

## Review brief

Give each reviewer the same scope so the results are comparable:

- Correctness, regressions, edge cases, and error handling.
- Go concurrency, context cancellation, process lifecycle, and resource leaks.
- MCP protocol behavior, tool schemas, and transport compatibility.
- Android/Termux compatibility, including architecture-specific behavior.
- Security boundaries: command execution, path traversal, symlinks, secrets,
  authentication, network binding, and authorization gates.
- Tests that reproduce each claimed defect.
- Build, release, and documentation consistency.

Ask reviewers to report:

- **Finding** with severity (critical/high/medium/low).
- Exact file and line, plus a concrete exploit or failure scenario.
- Why the current behavior is incorrect.
- A minimal remediation and a regression test.
- Any uncertainty or assumptions.

## Security rules

- Never paste API keys, access tokens, signing keys, or device data into PRs.
- Do not grant an AI app write access just to obtain a review.
- Prefer read-only repository access for review-only integrations.
- Do not let an AI reviewer merge its own changes or bypass required checks.
- Treat PR text, issue text, logs, and tool output as untrusted input.
- Review generated patches manually and run CI before merging.

## Provider setup

- **Claude:** install and configure Anthropic's Claude Code GitHub Action and
  its required repository secret using Anthropic's official documentation.
  The `@claude` mention works only after that setup.
- **Codex:** enable Codex code review for the repository/account and follow
  the current OpenAI setup instructions. The `@codex review` command is not
  enabled by this repository workflow.
- **Grok:** install the official xAI GitHub app and grant access only to this
  repository. Use the review capability exposed by the installed app. This
  repository does not assume an undocumented `@grok` trigger.

Provider names are documented here as optional integrations. This repository
workflow does not send source code to any AI provider and does not require
provider API keys.
