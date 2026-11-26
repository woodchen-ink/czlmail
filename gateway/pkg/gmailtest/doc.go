// Package gmailtest is an in-memory stand-in for one Gmail account: an IMAP server
// with the X-GM-EXT-1 subset the gateway relies on, and an httptest Gmail API
// (users.messages.send, settings.sendAs.list). Test code only.
//
// The IMAP side models Gmail's label store: every message lives once, folders are
// views over labels, and each folder numbers its messages with its own UIDs. That is
// what makes the jmap-perl#125 traps reproducible:
//
//   - A UID only means something in the folder it came from. After APPEND to
//     [Gmail]/Drafts, APPENDUID's UID may well exist in [Gmail]/All Mail as a
//     different message; the message has to be found there by X-GM-MSGID.
//   - Labels with spaces must be quoted. STORE +X-GM-LABELS (Work Projects) adds the
//     two labels "Work" and "Projects", exactly as Gmail does.
//   - The \Draft message flag and the \Drafts folder are one thing: a message is in
//     [Gmail]/Drafts exactly when it has \Draft, and X-GM-LABELS lists "\\Draft".
//
// Supported: CAPABILITY, ID, NAMESPACE, ENABLE (CONDSTORE), LOGIN, AUTHENTICATE
// (PLAIN, XOAUTH2 with SASL-IR), LIST / XLIST (SPECIAL-USE, RETURN (SPECIAL-USE
// STATUS)), STATUS, SELECT / EXAMINE (CONDSTORE), CREATE, DELETE, RENAME, APPEND
// (APPENDUID), [UID] FETCH (UID FLAGS INTERNALDATE RFC822.SIZE ENVELOPE BODY[...]
// RFC822 MODSEQ X-GM-MSGID X-GM-THRID X-GM-LABELS, CHANGEDSINCE), [UID] STORE
// (FLAGS, X-GM-LABELS, .SILENT), [UID] SEARCH (common keys, MODSEQ, X-GM-RAW,
// X-GM-MSGID, X-GM-THRID, X-GM-LABELS), [UID] COPY / MOVE (COPYUID), EXPUNGE,
// UID EXPUNGE, CLOSE, UNSELECT, IDLE, NOOP, LOGOUT. Options.ObjectID adds OBJECTID
// (EMAILID, THREADID, MAILBOXID), which real Gmail does not offer; Dovecot has no
// OBJECTID either, so this is where gateway code for that tier gets exercised.
//
// Not supported: BODYSTRUCTURE, QRESYNC, NOTIFY, ESEARCH, COMPRESS, non-ASCII folder
// names (modified UTF-7).
package gmailtest
