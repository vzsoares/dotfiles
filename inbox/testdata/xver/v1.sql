BEGIN TRANSACTION;
CREATE TABLE decision_options (
    decision_id TEXT NOT NULL REFERENCES decisions(id),
    letter TEXT NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (decision_id, letter)
);
INSERT INTO "decision_options" VALUES('7.1','a','x');
INSERT INTO "decision_options" VALUES('7.1','b','y');
INSERT INTO "decision_options" VALUES('7.g','a','ship');
INSERT INTO "decision_options" VALUES('7.g','b','hold');
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
INSERT INTO "decisions" VALUES('7.1','7',1,0,'s1','spec-s1','Old?','','b','open',0,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','','none','','',0);
INSERT INTO "decisions" VALUES('7.2','7',2,0,'s1','spec-s1','Free?','why','','open',1,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','','none','','',1);
INSERT INTO "decisions" VALUES('7.g','7',1,1,'s1','spec-s1','Gate?','','','answered',0,'pick','a','','go','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','sent','42','',3);
INSERT INTO "decisions" VALUES('8.1','8',1,0,'s2','spec-s2','Failed?','','','answered',0,'text','','later','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','failed','','send refused',2);
INSERT INTO "decisions" VALUES('8.2','8',2,0,'s2','spec-s2','Gone?','','','withdrawn',0,'','','','','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','2026-01-01T00:00:00+00:00','none','','',1);
CREATE INDEX decisions_session ON decisions (session_id, status);
COMMIT;
PRAGMA user_version = 1;
