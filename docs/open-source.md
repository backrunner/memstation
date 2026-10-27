# Open-source scope and publication policy

The public repository contains source code, reusable documentation, synthetic fixtures,
and selected product/brand assets. Local plans, review notes, validation reports,
benchmark JSON, prototype captures and model-generation records belong in ignored
`.agents/` storage. Raw datasets, model replies, credentials, logs and runtime data
remain in ignored `.artifacts/` or deployment-specific storage.

Public documentation must not depend on these local archives. Retain licenses,
source assets required by build/export scripts, and intentional README screenshots.
Do not publish generated records solely because they pass a credential scanner.

`.gitignore` and `.dockerignore` exclude local agent archives and runtime material.
Ignore rules do not remove files already tracked by Git; review tracked deletions
and staged filenames whenever moving material into local storage. Moving a tracked
file does not erase it from previously published Git history.

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
