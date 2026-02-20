CREATE TABLE IF NOT EXISTS receipt_photos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pallet_receipt_id INTEGER NOT NULL,
    photo_blob BLOB NOT NULL,
    photo_mime TEXT NOT NULL DEFAULT 'image/jpeg',
    photo_name TEXT NOT NULL DEFAULT 'photo.jpg',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (pallet_receipt_id) REFERENCES pallet_receipts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_receipt_photos_receipt ON receipt_photos(pallet_receipt_id);
