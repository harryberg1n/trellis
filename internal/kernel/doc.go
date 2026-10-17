// Package kernel is the pure core of trellis: events, the lane machine, the
// feature table, arm assignment and rule evaluation. It imports the standard
// library only and never touches the clock or the filesystem; the caller
// supplies Event.At and receives effects as events (see internal/layering).
package kernel
