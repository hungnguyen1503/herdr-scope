package domain

import "strings"

// chiefTagMax is how much of a label's first segment the group tag may name.
// The tag is an id that doubles as a synthesised header, not prose, and a
// header shares a cell with the workspace name below it.
const chiefTagMax = 16

// WorkerName is the worker a workspace label names: the last dot-delimited
// segment, so `<chief>.<worker>` and the legacy `<chief>.<repo>.<worker>` both
// yield the bare worker and a published token need not know which form it read.
// Empty when the label has no dot or names no worker: an untagged workspace
// publishes nothing, and the row loses the cell instead of showing a whole label.
func WorkerName(label string) string {
	i := strings.LastIndexByte(label, '.')
	if i < 0 {
		return ""
	}
	return label[i+1:]
}

// ChiefTag is the group a workspace's label names. A worker workspace reads
// `<chief>.<repo>.<worker>`; the first dot-delimited segment is the tag and
// the rest of the label is opaque. Empty when the label has no dot or names
// no tag: an untagged workspace keeps the worktree grouping it always had.
func ChiefTag(label string) string {
	tag, _, ok := strings.Cut(label, ".")
	if !ok || tag == "" {
		return ""
	}
	// The cap is in runes: half a multibyte character would publish invalid
	// UTF-8 into a header token.
	if runes := []rune(tag); len(runes) > chiefTagMax {
		tag = string(runes[:chiefTagMax])
	}
	return tag
}
