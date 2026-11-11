PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE decision_briefs (
    session_id TEXT NOT NULL,
    task TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    brief TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task)
);
INSERT INTO decision_briefs VALUES('s1','7','Zen board','What the board does.','2026-01-01T00:00:00+00:00');
INSERT INTO decision_briefs VALUES('s2','8','Other','','2026-01-01T00:00:00+00:00');
CREATE TABLE decision_options (
    decision_id TEXT NOT NULL REFERENCES decisions(id),
    letter TEXT NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (decision_id, letter)
);
INSERT INTO decision_options VALUES('7.1','a','left');
INSERT INTO decision_options VALUES('7.1','b','right');
INSERT INTO decision_options VALUES('7.g','a','yes');
INSERT INTO decision_options VALUES('7.g','b','no');
INSERT INTO decision_options VALUES('8.1','a','a');
INSERT INTO decision_options VALUES('8.1','b','b');
INSERT INTO decision_options VALUES('9.1','a','Approve');
INSERT INTO decision_options VALUES('9.1','b','Hold');
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
    rev INTEGER NOT NULL DEFAULT 0,
    UNIQUE (task, gate, seq)
);
INSERT INTO decisions VALUES('7.1','7',1,0,'s1','spec-s1','Which way?','ctx','a','open',0,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','','none','','',0);
INSERT INTO decisions VALUES('7.2','7',2,0,'s1','spec-s1','Free now?','more','','open',1,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','','none','','',1);
INSERT INTO decisions VALUES('7.g','7',1,1,'s1','spec-s1','Gate?','','','answered',0,'pick','a','','go','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','held','','',1);
INSERT INTO decisions VALUES('8.1','8',1,0,'s2','spec-s2','Failed?','','','answered',0,'text','','later','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','failed','','agent-manager not found',3);
INSERT INTO decisions VALUES('8.2','8',2,0,'s2','spec-s2','Gone?','','','withdrawn',0,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','none','','',1);
INSERT INTO decisions VALUES('9.1','9',1,0,'s2','spec-s2','release dotfiles master@0123456789ab?','','a','open',0,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','','none','','',0);
CREATE TABLE prod_actions (
    decision_id TEXT PRIMARY KEY REFERENCES decisions(id),
    action TEXT NOT NULL,
    repo TEXT NOT NULL,
    branch TEXT NOT NULL,
    sha TEXT NOT NULL,
    used_at TEXT NOT NULL DEFAULT '',
    used_by TEXT NOT NULL DEFAULT ''
);
INSERT INTO prod_actions VALUES('9.1','release','dotfiles','master','0123456789abcdef0123456789abcdef01234567','','');
CREATE TABLE decision_drafts (
    decision_id TEXT PRIMARY KEY REFERENCES decisions(id),
    kind TEXT NOT NULL,
    option TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    rev INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO decision_drafts VALUES('7.1','pick','a','',0,'2026-01-01T00:00:00+00:00');
CREATE INDEX decisions_session ON decisions (session_id, status);
COMMIT;
PRAGMA user_version = 4;
