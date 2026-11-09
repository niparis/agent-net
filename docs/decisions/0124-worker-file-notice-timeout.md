# 0124 - Deliver worker notices when files are missing

Date: 2026-11-09

## Context

A worker notice can be stored before its files are offered. A route lookup or
transfer can then fail, leaving the caller waiting for the report until the
message expires. A caller needs the report even if the files do not arrive.

## Decision

The sender retries failed file offers for a stored worker notice with the same
message key and checked snapshots. The receiver waits at most two minutes from
when the gateway stored the notice. If files are still missing, or collection
fails, it delivers the notice with their names and the files that arrived. If
the receiving daemon was offline, it applies the deadline on reconnect.
The receiver logs the failure. A replay after a restart keeps the same deadline.

Ordinary messages still wait for their files until their existing expiry. The
receiver still checks each file's size and hash before taking it. This changes
the worker notice exception to the waiting rule in decision 0054.
