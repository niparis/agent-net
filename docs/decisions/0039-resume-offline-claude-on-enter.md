# 0039 - Enter resumes an offline Claude session

**Date:** 2026-11-01
**Status:** Accepted.

Decision 0032 made Enter and `e` run the session enter plan. An offline
Claude session on this machine must first be restarted before it can be
entered. Enter and `e` now resume it in the background and enter the session
when it registers. If the row has been offline for more than an hour, the
result line asks for confirmation. Escape cancels. A missing folder is
reported. Claude may resume as a copy with a new native session ID; the new
session is entered.

Offline OpenCode and Codex rows keep their existing enter plans. Other
offline harnesses and remote Claude rows cannot yet be resumed this way.
The resume method is owner-only, like starting an owner session.
