# The CLI is bash

Every command in the CLI is `curl`, `jq`, `yq`, `systemctl`, or `yay`. Choosing AUR (ADR-0003)
removed the download-and-verify logic that would have justified Go or Rust, and the override
merge (ADR-0005) is a single `yq eval-all 'select(fi==0) * select(fi==1)'`.

Bash ships inside the plugin folder with no build step and no release pipeline, which means the
panel and the CLI can never drift to different versions — they are the same git checkout. It
may end up split across several scripts rather than one.

## Consequences

If subscription handling later grows teeth, porting is a day's work on something whose behaviour
is already pinned down. The real risk is not size but testability: bash resists it, so the
seams that matter should stay small and text-in/text-out.
