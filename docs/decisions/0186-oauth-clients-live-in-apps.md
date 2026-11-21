# 0186 - OAuth clients live in the apps table

Status: accepted by the owner on 2026-11-21 ("yes" in the schema review).

## Problem

`apps` and `oauth_clients` held the same client id, client name and redirect
addresses. An app always has one client. A client lives alone only in the hour
between its registration and the owner's approval. `app_refresh_budget` counted
rotations that `app_tokens` already records.

## Decision

- A client is a row of `apps` in the `registered` state until the owner approves
  it. Approval sets its name and kind and the state becomes `approved`. A client
  that already has an app gets a second row at its next approval, as before.
- `apps` gains `grant_types`, `response_types`, `token_endpoint_auth_method`,
  `source` and `state`. A registered row has an empty name and a placeholder
  kind. Every reader that lists apps, changes an app or issues a token checks
  that the state is `approved`.
- The unique name rule covers approved, active apps only. Two registered rows may
  both have the empty name.
- The pairing code lookup and the approval no longer need a `phone` client. A
  phone pairing is an app with its own name.
- The rotation limit counts the family's used refresh hashes from the last 24
  hours. It is a sliding window, so it is as strict as the old fixed window or
  stricter. The limit, the lifetimes and the other sign-in rules do not change.
- `oauth_clients` and `app_refresh_budget` are dropped by one migration at the end
  of the list.

## Why

Fewer tables and no copy of the client to keep in step. The only new state is a
column. Nothing the owner sees changes.
