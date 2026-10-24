# Open-source publication scope

Reviewed on **2026-10-25**, starting at commit `23f8623`. This records the evidence
for opening the repository, not a certification that every future commit or binary
distribution is free of security or licensing issues.

## Source and history

- The initial checkout contained 740 tracked files and 83 reachable commits on
  `main` / `origin/main`. Gitleaks **8.30.1** scanned all reachable history with
  redacted output and inline allow comments disabled: **zero findings**. It examined
  82 patches, approximately 25.59 MB.
- A separate Git object pass inspected 1,875 reachable blobs, including 12 binary
  assets, and checked text for local machine paths, private key headers and contact
  information. No personal machine paths or private keys were found. Text contacts
  were synthetic fixture addresses. Git commit author identity remains part of the
  normal contributor attribution history.
- The tracked files and history contain configuration examples and synthetic test
  credentials, not the local Ollama environment, actual client credentials, service
  logs, database backups or downloaded model weights. Known development defaults
  are labeled as local-only and must be replaced for exposed deployments.
- Native golden data and the small LOCOMO/LongMemEval-shaped fixtures are synthetic.
  Downloaded upstream datasets and evaluation artifacts are not distributed.
- Current screenshots are captured from the repository fixture server. Planning
  screenshots under `docs/plans/` remain explicitly separate from actual product UI.

Local environment files, `.agents/`, `.artifacts/`, dependencies and build output
remain outside Git. Ignore rules also cover private key containers, common database
and backup formats, logs, local model weights and root-level data directories.
Docker context exclusions cover the same sensitive material. Neither mechanism
removes a file that is already tracked, so staged changes still require review.

The complete staged export was scanned again before publication: approximately
7.11 MB, **zero findings**. Both READMEs rendered through GitHub's Markdown API;
local document links, Issue form YAML, shell snippet syntax and Compose config
parsing passed. The screenshot script ran against isolated fixtures and checked
390px overflow. This documentation change did not repeat the prior full application
regression or rebuild the full Compose stack.

## GitHub material

The publication review also covers the repository description, issue/PR surfaces,
release assets and retained Actions logs and diagnostic artifacts. The repository
had no issues, pull requests or releases at review time. All **58 runs within the
90-day retention window** and all **6 retained artifacts** were downloaded. Logs
and artifact content, including nested Playwright trace archives, were scanned with
redacted output. The nested-archive scan reported one generic API-key finding. It
was traced to the fixture server's `nextAPIKey` function and the loopback-only
credential-issuance test response: a random synthetic credential, not a key for a
live deployment or upstream provider. No real credential finding remained after
that review. Expired Actions logs are not accessible through the API and are
outside the available evidence.

Raw audit reports and downloaded archives stay in ignored `.artifacts/` storage;
they are not added to the public repository. Future logs and uploads should use
synthetic data and short retention. Do not upload local runtime artifacts just
because a file passes a credential scanner.

## License choice

MemStation retains **Apache License 2.0**, already applied to the project before
publication. It supports commercial use, modification and redistribution under
its conditions, includes an express patent grant, and preserves copyright and
NOTICE obligations. This fits a self-hosted gateway intended for integrations and
future commercial products. See the [official Apache text](https://www.apache.org/licenses/LICENSE-2.0)
and its [OSI listing](https://opensource.org/license/apache-2.0).

The original source, documentation and brand assets use Apache-2.0. Third-party
dependencies and fonts retain their own licenses, including MPL-2.0 and SIL OFL.
The project license does not relicense those files. [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md)
records the installed dependency inventory and source-availability references;
distribution must preserve each dependency's actual terms and notices.

LOCOMO upstream data is CC BY-NC 4.0 and must be obtained separately. Other datasets,
model weights and hosted APIs retain their own terms. No model access or external
dataset rights are granted by the MemStation repository license.

## Repeating the check

Before publishing a new branch or release, review `git status`, staged filenames,
configuration examples, screenshots, generated archives and dependency changes.
Scan reachable history with:

```bash
gitleaks git --log-opts="--all --full-history" --redact --ignore-gitleaks-allow
```

Also scan a clean export of the staged tree. Keep redacted reports private and
inspect any finding before proceeding. Use [SECURITY.md](../SECURITY.md) for
vulnerabilities and [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution checks.
