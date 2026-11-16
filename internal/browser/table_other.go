//go:build !darwin && !windows

package browser

// table is empty on systems bx has no browser layout for yet (Linux). bx
// still builds there, but no browser is found; a port adds a table like
// table_darwin.go or table_windows.go.
var table = []Browser{}
