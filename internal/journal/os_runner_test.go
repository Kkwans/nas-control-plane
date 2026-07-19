package journal

import "testing"

func TestIsNoEntriesExitRecognizesOnlyJournalctlExitOne(t *testing.T) {
	if !isNoEntriesExit(journalExitError(1)) {
		t.Fatal("exit status 1 should represent an empty journal query")
	}
	if isNoEntriesExit(journalExitError(2)) {
		t.Fatal("exit status 2 must remain a command failure")
	}
}

type journalExitError int

func (e journalExitError) Error() string {
	return "journal exit"
}

func (e journalExitError) ExitCode() int {
	return int(e)
}
