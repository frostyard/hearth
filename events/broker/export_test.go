package broker

// Buffered returns how many bytes the connection holds for the broker that it
// cannot send yet: while it reconnects, that is the publish that waits for the
// connection.
//
// It is the test's view of the client's state, and no production caller needs
// it, so it lives in a test file.
func (t *Transport) Buffered() (int, error) {
	return t.conn.Buffered()
}
