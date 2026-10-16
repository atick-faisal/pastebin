# Contributing

Thanks for helping. This is a small project, so the process is small too.

## Before you start

Read [docs/goal.md](docs/goal.md). It says what the project is for and what it
deliberately is **not**. Changes that add any of these will not be accepted:

- Merging of concurrent edits (CRDT, operational transformation).
- Accounts, authentication or private access.
- Analytics or observability features.
- Persistence across restarts.
- UI work beyond the minimal terminal-style interface.

If you are unsure, open an issue first and ask.

## Making a change

1. Fork the repo and make a branch.
2. Keep the change small and focused. Follow the style of the code around it.
3. Add or update tests. Run `just verify` and make sure it passes. If you
   touched the browser code, also run `just browser-check`.
4. Open a pull request and fill in the template.

[AGENTS.md](AGENTS.md) lists every `just` recipe.

## Commit messages

The history uses an emoji, a conventional type and a scope, for example
`✨ feat(web): add Alt keyboard shortcuts`. Match that style.

## Reporting bugs and security issues

Use the issue templates for bugs and ideas. For a security problem, follow
[SECURITY.md](SECURITY.md) instead of opening a public issue.

## Licensing

By contributing, you agree that your work is licensed under the
[Apache License, Version 2.0](LICENSE), the same as the rest of the project.

## Conduct

Be kind. Everyone taking part must follow the
[Code of Conduct](CODE_OF_CONDUCT.md).
