//go:build !windows

package sysproxy

// No crash guard outside Windows: the app runs in a terminal and restores on
// Ctrl+C. `dpi-bypass -restore` fixes a leftover setting.
func StartGuard()  {}
func RunGuard(int) {}
