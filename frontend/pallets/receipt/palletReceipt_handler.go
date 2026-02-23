package receipt

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/cache"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

// ReceiptPageQueryHandler renders the receipt screen for a pallet.
func ReceiptPageQueryHandler(db *sqlite.DB, _ *cache.UserSessionCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		session, _ := context.GetSessionFromContext(r.Context())
		data, err := LoadPageData(r.Context(), db, id)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "pallet not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to load receipt page", http.StatusInternalServerError)
			return
		}
		for _, role := range session.UserRoles {
			if role == rbac.RoleAdmin {
				data.IsAdmin = true
				break
			}
		}
		data.CanEdit = CanUserReceiptPallet(data.ProjectStatus, data.PalletStatus, session.UserRoles)
		if !data.CanEdit {
			if data.ProjectStatus != "active" {
				data.Message = "Project is inactive. This pallet is read-only."
			} else if data.PalletStatus == "cancelled" {
				data.Message = "Pallet is cancelled. This pallet is read-only."
			} else {
				data.Message = "Pallet is closed. Only admins can add or edit receipt lines."
			}
		}
		if msg := strings.TrimSpace(r.URL.Query().Get("error")); msg != "" {
			data.Message = msg
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := PalletReceiptPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render receipt page", http.StatusInternalServerError)
			return
		}
	}
}

// CreateReceiptCommandHandler stores/merges receipt line against pallet.
func CreateReceiptCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}

		if err := parseReceiptForm(r); err != nil {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
			return
		}

		session, _ := context.GetSessionFromContext(r.Context())
		palletStatus, _, projectStatus, err := LoadPalletContext(r.Context(), db, id)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "pallet not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to load pallet", http.StatusInternalServerError)
			return
		}
		if !CanUserReceiptPallet(projectStatus, palletStatus, session.UserRoles) {
			msg := "closed pallets can only be edited by admins"
			if projectStatus != "active" {
				msg = "inactive projects are read-only"
			} else if palletStatus == "cancelled" {
				msg = "cancelled pallets are read-only"
			}
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}

		qty, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("qty")), 10, 64)
		if err != nil || qty <= 0 {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("qty must be greater than 0"), http.StatusSeeOther)
			return
		}
		caseSize, err := strconv.ParseInt(strings.TrimSpace(defaultOne(r.FormValue("case_size"))), 10, 64)
		if err != nil || caseSize <= 0 {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("case size must be greater than 0"), http.StatusSeeOther)
			return
		}

		damagedQty, err := strconv.ParseInt(strings.TrimSpace(defaultZero(r.FormValue("damaged_qty"))), 10, 64)
		if err != nil || damagedQty < 0 {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("damaged qty must be 0 or greater"), http.StatusSeeOther)
			return
		}
		damaged := r.FormValue("damaged") != "" || damagedQty > 0
		if damaged && damagedQty <= 0 {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("damaged qty is required when damaged is selected"), http.StatusSeeOther)
			return
		}
		if damagedQty > qty {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("damaged qty cannot exceed qty"), http.StatusSeeOther)
			return
		}

		expiry, err := parseDate(strings.TrimSpace(r.FormValue("expiry_date")))
		if err != nil {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("invalid expiry date"), http.StatusSeeOther)
			return
		}

		input := ReceiptInput{
			PalletID:       id,
			SKU:            strings.TrimSpace(r.FormValue("sku")),
			Description:    strings.TrimSpace(r.FormValue("description")),
			Qty:            qty,
			CaseSize:       caseSize,
			Damaged:        damaged,
			DamagedQty:     damagedQty,
			BatchNumber:    strings.TrimSpace(r.FormValue("batch_number")),
			ExpiryDate:     expiry,
			CartonBarcode:  strings.TrimSpace(r.FormValue("carton_barcode")),
			ItemBarcode:    strings.TrimSpace(r.FormValue("item_barcode")),
			NoOuterBarcode: r.FormValue("no_outer_barcode") != "",
			NoInnerBarcode: r.FormValue("no_inner_barcode") != "",
		}

		if blob, mimeType, fileName, err := parseOptionalPhoto(r); err != nil {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		} else {
			input.StockPhotoBlob = blob
			input.StockPhotoMIME = mimeType
			input.StockPhotoName = fileName
		}

		photos, err := parseOptionalPhotos(r)
		if err != nil {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		input.Photos = photos

		if input.SKU == "" {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("sku is required"), http.StatusSeeOther)
			return
		}

		if err := SaveReceipt(r.Context(), db, auditSvc, session.UserID, input); err != nil {
			http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt?error="+url.QueryEscape("failed to save receipt"), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/tasker/pallets/"+strconv.FormatInt(id, 10)+"/receipt", http.StatusSeeOther)
	}
}

func CanUserReceiptPallet(projectStatus, palletStatus string, userRoles []string) bool {
	if projectStatus != "active" {
		return false
	}
	if palletStatus == "cancelled" {
		return false
	}
	if palletStatus == "created" || palletStatus == "open" {
		return true
	}
	if palletStatus == "closed" {
		for _, role := range userRoles {
			if role == rbac.RoleAdmin {
				return true
			}
		}
		return false
	}
	return false
}

// SearchStockQueryHandler returns matching stock codes.
func SearchStockQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := context.GetSessionFromContext(r.Context())
		if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		q := r.URL.Query().Get("q")
		items, err := SearchStock(r.Context(), db, *session.ActiveProjectID, q)
		if err != nil {
			http.Error(w, "failed to search stock", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}
}

func parsePalletID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "id")
	return strconv.ParseInt(idStr, 10, 64)
}

func parseDate(v string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Parse("02/01/2006", v)
}

// ReceiptPhotoQueryHandler streams a stored stock photo for a receipt line.
func ReceiptPhotoQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		palletID, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		receiptID, err := strconv.ParseInt(chi.URLParam(r, "receiptID"), 10, 64)
		if err != nil || receiptID <= 0 {
			http.Error(w, "invalid receipt id", http.StatusBadRequest)
			return
		}

		blob, mimeType, fileName, err := LoadReceiptPhoto(r.Context(), db, palletID, receiptID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "failed to load photo", http.StatusInternalServerError)
			return
		}
		if len(blob) == 0 {
			http.NotFound(w, r)
			return
		}
		if strings.TrimSpace(mimeType) == "" {
			mimeType = http.DetectContentType(blob)
		}
		w.Header().Set("Content-Type", mimeType)
		if strings.TrimSpace(fileName) != "" {
			w.Header().Set("Content-Disposition", "inline; filename=\""+fileName+"\"")
		}
		_, _ = w.Write(blob)
	}
}

func defaultZero(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "0"
	}
	return v
}

func defaultOne(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "1"
	}
	return v
}

func parseOptionalPhoto(r *http.Request) (blob []byte, mimeType, fileName string, err error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
		return nil, "", "", nil
	}

	file, header, err := r.FormFile("stock_photo")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, "", "", nil
		}
		return nil, "", "", err
	}
	defer file.Close()

	const maxPhoto = 5 << 20 // 5MB
	data, err := io.ReadAll(io.LimitReader(file, maxPhoto+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) == 0 {
		return nil, "", "", nil
	}
	if len(data) > maxPhoto {
		return nil, "", "", errors.New("photo must be 5MB or less")
	}

	mimeType = strings.TrimSpace(header.Header.Get("Content-Type"))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", "", errors.New("photo must be an image file")
	}

	fileName = strings.TrimSpace(header.Filename)
	if fileName == "" {
		exts, _ := mime.ExtensionsByType(mimeType)
		ext := ""
		if len(exts) > 0 {
			ext = exts[0]
		}
		fileName = "stock-photo" + ext
	} else {
		fileName = filepath.Base(fileName)
	}

	return data, mimeType, fileName, nil
}

func parseReceiptForm(r *http.Request) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
		return r.ParseMultipartForm(50 << 20) // 50MB for multiple photos
	}
	return r.ParseForm()
}

func parseOptionalPhotos(r *http.Request) ([]PhotoInput, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File["stock_photos"]
	if len(files) == 0 {
		return nil, nil
	}

	const maxPhoto = 5 << 20 // 5MB per photo
	var photos []PhotoInput
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxPhoto+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		if len(data) > maxPhoto {
			return nil, errors.New("each photo must be 5MB or less")
		}

		mimeType := strings.TrimSpace(fh.Header.Get("Content-Type"))
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, errors.New("photos must be image files")
		}

		fileName := strings.TrimSpace(fh.Filename)
		if fileName == "" {
			exts, _ := mime.ExtensionsByType(mimeType)
			ext := ""
			if len(exts) > 0 {
				ext = exts[0]
			}
			fileName = "stock-photo" + ext
		} else {
			fileName = filepath.Base(fileName)
		}

		photos = append(photos, PhotoInput{Blob: data, MIMEType: mimeType, FileName: fileName})
	}
	return photos, nil
}

// ReceiptPhotosHandler serves a photo from the receipt_photos table.
func ReceiptPhotosHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		palletID, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		receiptID, err := strconv.ParseInt(chi.URLParam(r, "receiptID"), 10, 64)
		if err != nil || receiptID <= 0 {
			http.Error(w, "invalid receipt id", http.StatusBadRequest)
			return
		}
		photoID, err := strconv.ParseInt(chi.URLParam(r, "photoID"), 10, 64)
		if err != nil || photoID <= 0 {
			http.Error(w, "invalid photo id", http.StatusBadRequest)
			return
		}

		blob, mimeType, fileName, err := LoadReceiptPhotoByID(r.Context(), db, palletID, receiptID, photoID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "failed to load photo", http.StatusInternalServerError)
			return
		}
		if len(blob) == 0 {
			http.NotFound(w, r)
			return
		}
		if strings.TrimSpace(mimeType) == "" {
			mimeType = http.DetectContentType(blob)
		}
		w.Header().Set("Content-Type", mimeType)
		if strings.TrimSpace(fileName) != "" {
			w.Header().Set("Content-Disposition", "inline; filename=\""+fileName+"\"")
		}
		_, _ = w.Write(blob)
	}
}
