PRAGMA foreign_keys = OFF;

BEGIN TRANSACTION;

DROP TABLE IF EXISTS pallets__new;

CREATE TABLE IF NOT EXISTS pallets__new (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('created', 'open', 'closed', 'labelled', 'cancelled')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at DATETIME,
    reopened_at DATETIME,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

INSERT INTO pallets__new (id, project_id, status, created_at, closed_at, reopened_at)
SELECT
    id,
    project_id,
    CASE
        WHEN status IN ('created', 'open', 'closed', 'labelled', 'cancelled') THEN status
        ELSE 'created'
    END AS status,
    created_at,
    closed_at,
    reopened_at
FROM pallets;

DROP TABLE pallets;
ALTER TABLE pallets__new RENAME TO pallets;

CREATE INDEX IF NOT EXISTS idx_pallets_project_id ON pallets(project_id);
CREATE INDEX IF NOT EXISTS idx_pallets_status ON pallets(status);

COMMIT;

PRAGMA foreign_keys = ON;
