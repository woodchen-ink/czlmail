// Package imaptest connects tests to the Dovecot servers in docker-compose.test.yml
// and gives them a raw IMAP line client. Test code only.
//
// The servers are found through environment variables; when one is not set the test
// is skipped, so `go test ./...` passes without Docker:
//
//	GATEWAY_TEST_IMAP_ADDR        full tier (CONDSTORE, QRESYNC, NOTIFY, ...), 127.0.0.1:1143
//	GATEWAY_TEST_IMAP_BASIC_ADDR  fallback tier (no CONDSTORE / QRESYNC / NOTIFY), 127.0.0.1:1144
//
// Every test should use its own user from Server.NewUser: Dovecot accepts any user name
// with the password "test" and gives it an empty mailbox, so tests never share state.
// The fixed users alice and bob exist for manual checks.
package imaptest
