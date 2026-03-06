# Task: CLI Framework (`hedgehogctl`) + Core Plumbing

## Purpose
The CLI is how real operators manage clusters under pressure. This task establishes the CLI binary, command routing, endpoint/auth config, and output formatting so subsequent tasks can add commands quickly without redesigning the CLI architecture.

## Scope
- Create CLI binary `hedgehogctl` (or repo-native name).
- Global flags: `--endpoint`, auth placeholders, `--output table|json`, `--yes`, `--timeout`, `--wait`.
- Implement HTTP client + error handling + consistent exit codes.

## Must-have
- Helpful `--help` with examples.
- JSON output stable for automation.
- Safe defaults; confirmations for destructive ops.

## Must-not
- Do not hardcode endpoints or credentials.

## Subagent steps
1. **CLI Engineer**: Build command skeleton and shared client.
2. **UX Engineer**: Output formatting + help text templates.
3. **Test Engineer**: Add smoke tests for CLI invocation.

## Test criteria
- CLI unit tests for parsing.
- Simple integration test hits `/v1/cluster` and prints output.

## Completion criteria
- CLI framework ready for subsequent command tasks.
