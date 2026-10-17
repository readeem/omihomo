# The CLI is part of the Go binary

`omihomo daemon` hosts the core; the other subcommands make typed socket requests. Sharing the protocol and error types keeps scripting consistent with the widget and removes the shell pipelines that used to own state.

Installation remains a short terminal script that invokes the package manager and `claim`. It never downloads or configures a standalone core.
