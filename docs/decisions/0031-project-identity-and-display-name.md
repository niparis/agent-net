# 0031 - Project identity and display name

**Date:** 2026-10-31
**Status:** Accepted.

## Context

A checkout folder may have a different name from the repository. Linked
worktrees and separate clones can also have different folder names. A folder
name made one repository appear as several projects and split its charter.

## Decision

The last path part of the `origin` remote, without `.git`, is the project
identity. With no usable origin, use the main tree folder name, or the session
folder outside a repository. Repositories with the same remote basename share
an identity. The identity is used for actions and graph relationships.

The gateway keeps an optional display name per identity. The owner can set or
clear it on Map with `D`, or ask the CTO to use `project_display_set`. The
operator get and set methods use the owner gate; the CTO uses its private
process-tree-checked channel. Project and agent listings carry both identity
and display name. Screens use the display name when present.

On upgrade, stored remote URLs connect old folder names to identities. The
migration moves project and graph data in one transaction. It keeps both
charter texts when they collide and records the merge in the gateway log. A
pair with conflicting graph controls is left unchanged and logged; other pairs
still migrate and the gateway starts. Ambiguous mappings are left unchanged.
The migration is applied once, so those cases need review.
