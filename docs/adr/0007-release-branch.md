# Omarchy installs the plugin from a release branch that only CI moves

`omarchy plugin add` clones the repository's default branch and `omarchy plugin update` fast-forwards to it; neither can follow a tag. The widget refuses a daemon that speaks another protocol, so a plugin ahead of the published package would leave users with an update they cannot install. The default branch is therefore `release`, and it only ever points at a published tag. Development lands on `next`.

The release workflow fast-forwards `release` to the tag after the package is public, in the approved `release` environment, with a deploy key that alone bypasses the branch's ruleset. It never moves the branch backwards, and refuses a tag that does not descend from it.

The widget's update runs `omarchy plugin update` first, then the updated checkout's installer for the package, so the package is verified with the new checkout's key and both halves land on the same release. A checkout Omarchy does not manage, such as a linked development checkout, is not touched.
