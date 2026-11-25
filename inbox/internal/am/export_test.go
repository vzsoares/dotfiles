package am

// TryWrite attempts a write through the read-only handle.
func TryWrite(s *State) error {
	_, err := s.db.Exec("INSERT INTO sessions (id, name, tool, cwd, group_name, status, created_at, last_status_at)" +
		" VALUES ('x', 'x', 'claude', '', '', 'idle', 0, 0)")
	return err
}
