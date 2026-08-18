package models

// export_test.go opens a door for the external models_test package into the
// package's unexported internals. Being a _test.go file, none of it exists in
// a build of the app — which is the point: a test needing one internal helper
// should not be a reason to export it to every caller of the package.

// ComputeBodyDiffForTest exposes the body-diff encoder. Sync tests need to
// construct a change that carries a diff rather than a snapshot, which is the
// shape a real spoke sends and the one relaying has to be careful with.
var ComputeBodyDiffForTest = computeBodyDiff
