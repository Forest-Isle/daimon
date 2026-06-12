---
name: clawhub
description: Search and install agent skills from ClawHub, the public skill registry.
homepage: https://clawhub.ai
tags:
  - registry
  - skills
  - install
---

# ClawHub

Public skill registry for AI agents. Search by natural language (vector search).

## When to use

Use this skill when the user asks any of:
- "find a skill for …"
- "search for skills"
- "install a skill"
- "what skills are available?"
- "update my skills"

## Search

```bash
clawhub search "web scraping" --limit 5
```

## Install

```bash
clawhub install <slug> --workdir ~/.daimon
```

Replace `<slug>` with the skill name from search results. This places the skill into `~/.daimon/skills/`, where Daimon loads workspace skills from. Always include `--workdir`.

## Update

```bash
clawhub update --all --workdir ~/.daimon
```

## List installed

```bash
clawhub list --workdir ~/.daimon
```

## Notes

- Requires `clawhub` CLI installed globally (`npm install -g clawhub`).
- No API key needed for search and install.
- Login (`clawhub login`) is only required for publishing.
- `--workdir ~/.daimon` is critical — without it, skills install to the current directory instead of the Daimon workspace.
- After install, remind the user to restart the agent session to load the new skill.
