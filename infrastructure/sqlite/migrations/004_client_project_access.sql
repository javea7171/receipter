CREATE TABLE IF NOT EXISTS client_project_access (
    user_id INTEGER NOT NULL,
    project_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, project_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Bootstrap access rows for existing client users.
INSERT OR IGNORE INTO client_project_access (user_id, project_id, created_at)
SELECT id, client_project_id, CURRENT_TIMESTAMP
FROM users
WHERE role = 'client'
  AND client_project_id IS NOT NULL
  AND client_project_id > 0;

CREATE INDEX IF NOT EXISTS idx_client_project_access_project_id ON client_project_access(project_id);
