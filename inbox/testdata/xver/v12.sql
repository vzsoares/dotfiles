PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE decisions (
    id TEXT PRIMARY KEY,
    task TEXT NOT NULL,
    seq INTEGER NOT NULL,
    gate INTEGER NOT NULL DEFAULT 0,
    session_id TEXT NOT NULL,
    session_name TEXT NOT NULL,
    question TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    rec TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    amended INTEGER NOT NULL DEFAULT 0,
    answer_kind TEXT NOT NULL DEFAULT '',
    answer_option TEXT NOT NULL DEFAULT '',
    answer_text TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    answered_at TEXT NOT NULL DEFAULT '',
    delivery TEXT NOT NULL DEFAULT 'none',
    message_id TEXT NOT NULL DEFAULT '',
    delivery_error TEXT NOT NULL DEFAULT '',
    rev INTEGER NOT NULL DEFAULT 0, body TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '', confirmed_at TEXT NOT NULL DEFAULT '',
    UNIQUE (task, gate, seq)
);
INSERT INTO decisions VALUES('933.1','933',1,0,'s1','spec-933','Queue or fail?','','a','answered',0,'pick','a','','go','2026-01-01T00:00:00+00:00','2026-01-01T00:03:00+00:00','2026-01-01T00:03:00+00:00','held','','',1,'see spec','','');
INSERT INTO decisions VALUES('933.2','933',2,0,'s1','spec-933','Restart the cockpit','','a','open',0,'','','','','2026-01-01T00:01:00+00:00','2026-01-01T00:01:00+00:00','','none','','',0,'','manual','');
INSERT INTO decisions VALUES('933.3','933',3,0,'s1','spec-933','Still open?','','','open',0,'','','','','2026-01-01T00:02:00+00:00','2026-01-01T00:02:00+00:00','','none','','',0,'','','');
INSERT INTO decisions VALUES('934.1','934',1,0,'s1','spec-933','push dotfiles master@0123456789ab?','','a','open',0,'','','','','2026-01-01T00:10:00+00:00','2026-01-01T00:10:00+00:00','','none','','',0,'','','');
CREATE TABLE decision_options (
    decision_id TEXT NOT NULL REFERENCES decisions(id),
    letter TEXT NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (decision_id, letter)
);
INSERT INTO decision_options VALUES('933.1','a','queue');
INSERT INTO decision_options VALUES('933.1','b','fail');
INSERT INTO decision_options VALUES('933.2','a','Done');
INSERT INTO decision_options VALUES('933.2','b','Later');
INSERT INTO decision_options VALUES('934.1','a','Approve');
INSERT INTO decision_options VALUES('934.1','b','Hold');
CREATE TABLE decision_briefs (
    session_id TEXT NOT NULL,
    task TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    brief TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task)
);
INSERT INTO decision_briefs VALUES('s1','933','Queue for a dead session','chief dies','2026-01-01T00:00:00+00:00');
CREATE TABLE prod_actions (
    decision_id TEXT PRIMARY KEY REFERENCES decisions(id),
    action TEXT NOT NULL,
    repo TEXT NOT NULL,
    branch TEXT NOT NULL,
    sha TEXT NOT NULL,
    used_at TEXT NOT NULL DEFAULT '',
    used_by TEXT NOT NULL DEFAULT ''
, cmd TEXT NOT NULL DEFAULT '', pushed_sha TEXT NOT NULL DEFAULT '', pushed_at TEXT NOT NULL DEFAULT '', pushed_by TEXT NOT NULL DEFAULT '');
INSERT INTO prod_actions VALUES('934.1','push','dotfiles','master','0123456789abcdef0123456789abcdef01234567','','','','','','');
CREATE TABLE decision_drafts (
    decision_id TEXT PRIMARY KEY REFERENCES decisions(id),
    kind TEXT NOT NULL,
    option TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    rev INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO decision_drafts VALUES('933.3','text','','later',0,'2026-01-01T00:09:00+00:00');
CREATE TABLE notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL,
    session_name TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    read_at TEXT NOT NULL DEFAULT '',
    reply TEXT NOT NULL DEFAULT '',
    replied_at TEXT NOT NULL DEFAULT '',
    delivery TEXT NOT NULL DEFAULT 'none',
    message_id TEXT NOT NULL DEFAULT '',
    delivery_error TEXT NOT NULL DEFAULT ''
, claimed_at TEXT NOT NULL DEFAULT '');
INSERT INTO notes VALUES(1,'933','c1','chief','Spec ready','read it','2026-01-01T00:04:00+00:00','2026-01-01T00:08:00+00:00','thanks','2026-01-01T00:08:00+00:00','sent','43','','2026-01-01T00:08:00+00:00');
INSERT INTO notes VALUES(2,'','c1','chief','Loose note','','2026-01-01T00:05:00+00:00','','','','none','','','');
CREATE TABLE note_replies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    note_id INTEGER NOT NULL,
    text TEXT NOT NULL,
    replied_at TEXT NOT NULL
);
INSERT INTO note_replies VALUES(1,1,'thanks','2026-01-01T00:08:00+00:00');
CREATE TABLE chats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    sent_at TEXT NOT NULL,
    message_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);
INSERT INTO chats VALUES(1,'933','how is it going?','2026-01-01T00:06:00+00:00','42','delivered','');
INSERT INTO chats VALUES(2,'','ping','2026-01-01T00:07:00+00:00','','failed','no live session named ''chief''');
PRAGMA writable_schema=ON;
CREATE TABLE IF NOT EXISTS sqlite_sequence(name,seq);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('note_replies',1);
INSERT INTO sqlite_sequence VALUES('notes',2);
INSERT INTO sqlite_sequence VALUES('chats',2);
CREATE INDEX decisions_session ON decisions (session_id, status);
CREATE INDEX note_replies_note ON note_replies (note_id, id);
CREATE INDEX chats_task ON chats (task, id);
PRAGMA writable_schema=OFF;
COMMIT;
PRAGMA user_version = 12;
