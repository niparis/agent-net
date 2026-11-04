// Package delivery holds the parts of the delivery pipeline that agent-net
// checks itself. See docs/design/delivery-pipeline.md.
//
// The tests_added evidence field is detected here: ChangedFiles lists what a
// change touched and how many lines each file gained, and TestsAdded says
// whether test files gained lines and whether any code changed.
//
// Every git call goes through one helper (git.go). Commits are named only by
// full commit IDs, and a repository with replace refs or a grafts file is
// refused.
package delivery
