// Package delivery holds the parts of the delivery pipeline that agent-net
// checks itself. See docs/design/delivery-pipeline.md.
//
// The tests_added evidence field is detected here: ChangedFiles lists what a
// change touched, and TestsAdded says whether that includes a test file.
package delivery
