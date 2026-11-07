# Harness adapters

Harnesses update on their own schedules. Routes and fields can change without an agent-net release. Each harness has an adapter. The daemon calls named operations and the adapter handles that harness's API.

Start with OpenCode. Read its OpenAPI document when a service starts. Select operations by the routes it advertises, not by version comparisons. Cache the result for the service lifetime and detect again when it restarts or its version changes. When the document is absent, use the older routes.

When an operation is missing, report the harness, its version and the operation. Publish the version and missing operations in the machine's harness inventory.

Keep old routes as fallbacks for about 30 days after the last machine runs the old version. Mark each fallback with the date and version it serves. Do not remove a fallback as part of this change. Other harnesses will get adapters in later work.
