package models

import (
	"time"

	"github.com/uptrace/bun"
)

// User represents an authenticated app user.
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID           int64     `bun:"id,pk,autoincrement"`
	Username     string    `bun:"username,unique,notnull"`
	PasswordHash string    `bun:"password_hash,notnull"`
	Role         string    `bun:"role,notnull"`
	CreatedAt    time.Time `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt    time.Time `bun:"updated_at,notnull,default:current_timestamp"`
}

// Session is used by middleware and auth handlers.
type Session struct {
	bun.BaseModel `bun:"table:sessions,alias:s"`

	ID                string         `bun:"id,pk"`
	UserID            int64          `bun:"user_id,notnull"`
	User              User           `bun:"rel:belongs-to,join:user_id=id"`
	UserRoles         []string       `bun:"-"`
	ScreenPermissions map[string]int `bun:"-"`
	ExpiresAt         time.Time      `bun:"expires_at,notnull"`
	CreatedAt         time.Time      `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt         time.Time      `bun:"updated_at,notnull,default:current_timestamp"`
}

// Expired returns true when the session expiry time has passed.
func (s Session) Expired() bool {
	return time.Now().After(s.ExpiresAt)
}

// StockItem is the item master imported from CSV.
type StockItem struct {
	bun.BaseModel `bun:"table:stock_items,alias:si"`

	ID          int64     `bun:"id,pk,autoincrement"`
	SKU         string    `bun:"sku,notnull,unique"`
	Description string    `bun:"description,notnull"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt   time.Time `bun:"updated_at,notnull,default:current_timestamp"`
}

// Pallet tracks lifecycle and label identity.
type Pallet struct {
	bun.BaseModel `bun:"table:pallets,alias:p"`

	ID         int64      `bun:"id,pk"`
	Status     string     `bun:"status,notnull"`
	CreatedAt  time.Time  `bun:"created_at,notnull,default:current_timestamp"`
	ClosedAt   *time.Time `bun:"closed_at"`
	ReopenedAt *time.Time `bun:"reopened_at"`
}

// PalletReceipt stores stock lines recorded against a pallet.
type PalletReceipt struct {
	bun.BaseModel `bun:"table:pallet_receipts,alias:pr"`

	ID              int64     `bun:"id,pk,autoincrement"`
	PalletID        int64     `bun:"pallet_id,notnull"`
	StockItemID     int64     `bun:"stock_item_id,notnull"`
	ScannedByUserID int64     `bun:"scanned_by_user_id,notnull"`
	Qty             int64     `bun:"qty,notnull"`
	Damaged         bool      `bun:"damaged,notnull,default:false"`
	DamagedQty      int64     `bun:"damaged_qty,notnull,default:0"`
	BatchNumber     string    `bun:"batch_number"`
	ExpiryDate      time.Time `bun:"expiry_date,notnull"`
	CartonBarcode   string    `bun:"carton_barcode"`
	ItemBarcode     string    `bun:"item_barcode"`
	StockPhotoBlob  []byte    `bun:"stock_photo_blob"`
	StockPhotoMIME  string    `bun:"stock_photo_mime"`
	StockPhotoName  string    `bun:"stock_photo_name"`
	NoOuterBarcode  bool      `bun:"no_outer_barcode,notnull,default:false"`
	NoInnerBarcode  bool      `bun:"no_inner_barcode,notnull,default:false"`
	CreatedAt       time.Time `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt       time.Time `bun:"updated_at,notnull,default:current_timestamp"`
}

// ReceiptPhoto stores individual photos attached to a receipt line.
type ReceiptPhoto struct {
	bun.BaseModel `bun:"table:receipt_photos,alias:rp"`

	ID              int64     `bun:"id,pk,autoincrement"`
	PalletReceiptID int64     `bun:"pallet_receipt_id,notnull"`
	PhotoBlob       []byte    `bun:"photo_blob,notnull"`
	PhotoMIME       string    `bun:"photo_mime,notnull,default:'image/jpeg'"`
	PhotoName       string    `bun:"photo_name,notnull,default:'photo.jpg'"`
	CreatedAt       time.Time `bun:"created_at,notnull,default:current_timestamp"`
}

// AuditLog captures immutable change history for key operations.
type AuditLog struct {
	bun.BaseModel `bun:"table:audit_logs,alias:al"`

	ID         int64     `bun:"id,pk,autoincrement"`
	UserID     int64     `bun:"user_id,notnull"`
	Action     string    `bun:"action,notnull"`
	EntityType string    `bun:"entity_type,notnull"`
	EntityID   string    `bun:"entity_id,notnull"`
	BeforeJSON string    `bun:"before_json"`
	AfterJSON  string    `bun:"after_json"`
	CreatedAt  time.Time `bun:"created_at,notnull,default:current_timestamp"`
}
