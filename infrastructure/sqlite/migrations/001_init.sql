CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'scanner')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS stock_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sku TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pallets (
    id INTEGER PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('open', 'closed')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at DATETIME,
    reopened_at DATETIME
);

CREATE TABLE IF NOT EXISTS pallet_receipts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pallet_id INTEGER NOT NULL,
    stock_item_id INTEGER NOT NULL,
    qty INTEGER NOT NULL CHECK (qty > 0),
    damaged BOOLEAN NOT NULL DEFAULT 0,
    damaged_qty INTEGER NOT NULL DEFAULT 0 CHECK (damaged_qty >= 0 AND damaged_qty <= qty),
    batch_number TEXT,
    expiry_date DATETIME NOT NULL,
    carton_barcode TEXT,
    item_barcode TEXT,
    stock_photo_blob BLOB,
    stock_photo_mime TEXT,
    stock_photo_name TEXT,
    no_outer_barcode BOOLEAN NOT NULL DEFAULT 0,
    no_inner_barcode BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (pallet_id) REFERENCES pallets(id),
    FOREIGN KEY (stock_item_id) REFERENCES stock_items(id)
);

CREATE TABLE IF NOT EXISTS stock_import_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    inserted_count INTEGER NOT NULL DEFAULT 0,
    updated_count INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS export_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    export_type TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    before_json TEXT,
    after_json TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_stock_items_sku ON stock_items(sku);
CREATE INDEX IF NOT EXISTS idx_stock_items_description ON stock_items(description);
CREATE INDEX IF NOT EXISTS idx_pallet_receipts_pallet_id ON pallet_receipts(pallet_id);
CREATE INDEX IF NOT EXISTS idx_pallets_status ON pallets(status);
CREATE INDEX IF NOT EXISTS idx_audit_logs_entity ON audit_logs(entity_type, entity_id);
