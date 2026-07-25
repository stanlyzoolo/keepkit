package main

// restartHint is what keeptui prints when it cannot replace itself with the
// updated binary: always on Windows, where there is no exec, and on unix when
// path resolution or syscall.Exec failed. The update itself already succeeded,
// so this is an instruction to the user, not an error report.
//
// Shared by both platforms' restartSelf, which is why it lives in the untagged
// file — everything that actually resolves and execs a path is unix-only.
const restartHint = "keeptui updated — run keeptui again"
