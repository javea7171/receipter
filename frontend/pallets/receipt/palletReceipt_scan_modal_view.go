package receipt

func renderScanModalAssets() string {
	return `<dialog id="scan-modal" class="modal">
  <div class="modal-box max-w-3xl">
    <h3 class="text-lg font-semibold">Scan Barcode</h3>
    <div id="scan-reader" class="mt-3 h-72 w-full overflow-hidden rounded-lg bg-neutral text-neutral-content"></div>
    <p id="scan-status" class="mt-3 text-sm opacity-70">Camera idle</p>
    <div class="modal-action">
      <button class="btn btn-lg w-full" type="button" onclick="closeScanModal()">Close</button>
    </div>
  </div>
</dialog>
<script>
let scanTargetInput = null;
let quaggaRunning = false;
let onDetectedHandler = null;

function setScanStatus(msg) {
  const el = document.getElementById("scan-status");
  if (el) el.textContent = msg;
}

function loadQuaggaScript() {
  if (window.Quagga) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = "https://cdn.jsdelivr.net/npm/@ericblade/quagga2@1.8.4/dist/quagga.min.js";
    s.onload = resolve;
    s.onerror = reject;
    document.head.appendChild(s);
  });
}

async function openScanModal(targetInputID) {
  scanTargetInput = document.getElementById(targetInputID);
  const modal = document.getElementById("scan-modal");
  if (!modal) return;
  modal.showModal();
  setScanStatus("Starting camera...");
  try {
    await startScanner();
  } catch (err) {
    setScanStatus("Camera failed: " + (err && err.message ? err.message : err));
  }
}

function closeScanModal() {
  stopScanner();
  const modal = document.getElementById("scan-modal");
  if (modal && modal.open) modal.close();
  setScanStatus("Camera idle");
}

function closeReceiptLineEditor() {
  const modal = document.getElementById("receipt-line-editor-modal");
  if (modal && modal.open) modal.close();
}

function updateCommentStatus() {
  const input = document.getElementById("comment_input");
  const status = document.getElementById("comment_status");
  const openBtn = document.getElementById("comment_open_btn");
  if (!input || !status) return;
  const hasComment = input.value.trim() !== "";
  status.textContent = hasComment ? "Comment added" : "No comment";
  status.className = hasComment ? "text-sm text-success font-medium" : "text-sm text-base-content/60";
  if (openBtn) {
    openBtn.textContent = hasComment ? "Edit Comment" : "Add Comment";
  }
}

function openCommentModal() {
  const modal = document.getElementById("comment-modal");
  const input = document.getElementById("comment_input");
  const textarea = document.getElementById("comment_modal_text");
  if (!modal || !input || !textarea) return;
  textarea.value = input.value || "";
  modal.showModal();
  textarea.focus();
  textarea.setSelectionRange(textarea.value.length, textarea.value.length);
}

function closeCommentModal() {
  const modal = document.getElementById("comment-modal");
  if (modal && modal.open) modal.close();
}

function saveCommentValue() {
  const input = document.getElementById("comment_input");
  const textarea = document.getElementById("comment_modal_text");
  if (!input || !textarea) return;
  input.value = textarea.value.trim();
  updateCommentStatus();
  closeCommentModal();
}

function clearCommentValue() {
  const input = document.getElementById("comment_input");
  const textarea = document.getElementById("comment_modal_text");
  if (input) input.value = "";
  if (textarea) textarea.value = "";
  updateCommentStatus();
}

async function startScanner() {
  if (quaggaRunning) return;
  await loadQuaggaScript();
  const target = document.getElementById("scan-reader");
  if (!target) throw new Error("scan target missing");

  await new Promise((resolve, reject) => {
    window.Quagga.init({
      inputStream: {
        type: "LiveStream",
        target: target,
        constraints: {
          facingMode: { ideal: "environment" }
        }
      },
      decoder: {
        readers: ["code_128_reader", "ean_reader", "ean_8_reader", "upc_reader", "upc_e_reader"]
      },
      locate: true
    }, (err) => {
      if (err) return reject(err);
      return resolve();
    });
  });

  if (onDetectedHandler) {
    window.Quagga.offDetected(onDetectedHandler);
  }

  onDetectedHandler = function(result) {
    const code = result && result.codeResult && result.codeResult.code;
    if (!code || !scanTargetInput) return;
    scanTargetInput.value = code;
    closeScanModal();
  };
  window.Quagga.onDetected(onDetectedHandler);
  window.Quagga.start();
  quaggaRunning = true;
  setScanStatus("Point the camera at a barcode");
}

function stopScanner() {
  if (!window.Quagga || !quaggaRunning) return;
  if (onDetectedHandler) {
    window.Quagga.offDetected(onDetectedHandler);
  }
  window.Quagga.stop();
  quaggaRunning = false;
}

(function attachReceiptEnhancements() {
  const toggle = document.getElementById("damaged_toggle");
  const damagedFields = document.getElementById("damaged_fields");
  if (toggle && damagedFields) {
    toggle.addEventListener("click", function() {
      damagedFields.classList.toggle("hidden");
    });
  }

  const skuInput = document.getElementById("sku_input");
  const descriptionInput = document.getElementById("description_input");
  const uomInput = document.getElementById("uom_input");
  const suggestions = document.getElementById("sku_suggestions");
  const cartonBarcodeInput = document.getElementById("carton_barcode");
  const itemBarcodeInput = document.getElementById("item_barcode");
  const qtyInput = document.getElementById("qty_input");
  const caseSizeInput = document.getElementById("case_size_input");
  const batchInput = document.getElementById("batch_input");
  const expiryInput = document.getElementById("expiry_input");
  const unknownSkuToggle = document.getElementById("unknown_sku_toggle");
  const unknownSkuInput = document.getElementById("unknown_sku_input");
  const unknownSkuHint = document.getElementById("unknown_sku_hint");
  const lineEditorModal = document.getElementById("receipt-line-editor-modal");
  const lineEditorForm = document.getElementById("receipt-line-editor-form");
  const lineDeleteForm = document.getElementById("receipt-line-delete-form");
  updateCommentStatus();

  function closeSuggestions() {
    const list = document.getElementById("sku_suggestions");
    if (!list) return;
    list.innerHTML = "";
    list.classList.add("hidden");
  }

  if (skuInput && suggestions && !skuInput.disabled) {
    skuInput.addEventListener("keydown", function(event) {
      if (event.key === "Escape") {
        closeSuggestions();
      }
    });

    document.addEventListener("click", function(event) {
      if (event.target === skuInput) return;
      const list = document.getElementById("sku_suggestions");
      if (list && list.contains(event.target)) return;
      closeSuggestions();
    });
  }

  function setUnknownSkuFlag(enabled) {
    if (!unknownSkuInput) return;
    unknownSkuInput.value = enabled ? "1" : "";
    if (unknownSkuHint) {
      unknownSkuHint.classList.toggle("hidden", !enabled);
    }
    if (unknownSkuToggle) {
      unknownSkuToggle.classList.toggle("btn-warning", enabled);
      unknownSkuToggle.classList.toggle("btn-outline", !enabled);
      unknownSkuToggle.classList.toggle("text-white", enabled);
    }
  }

  if (unknownSkuToggle && unknownSkuInput) {
    unknownSkuToggle.addEventListener("click", function() {
      const next = unknownSkuInput.value !== "1";
      setUnknownSkuFlag(next);
      if (next) {
        if (skuInput && !skuInput.value.trim()) {
          skuInput.value = "UNKNOWN";
        }
        if (descriptionInput && !descriptionInput.value.trim()) {
          descriptionInput.value = "Unidentifiable item";
        }
        if (uomInput && !uomInput.value.trim()) {
          uomInput.value = "";
        }
        if (typeof openPhotoModal === "function") {
          openPhotoModal();
        }
      }
    });
  }

  if (skuInput && unknownSkuInput) {
    skuInput.addEventListener("input", function() {
      if (unknownSkuInput.value !== "1") return;
      const current = skuInput.value.trim().toUpperCase();
      if (current !== "" && current !== "UNKNOWN") {
        setUnknownSkuFlag(false);
      }
    });
  }

  function wireEnterFocus(from, to) {
    if (!from || !to) return;
    from.addEventListener("keydown", function(event) {
      if (event.key !== "Enter") return;
      event.preventDefault();
      if (to.disabled) return;
      to.focus();
      if (typeof to.select === "function" && to.type !== "date") {
        to.select();
      }
    });
  }

  wireEnterFocus(cartonBarcodeInput, itemBarcodeInput);
  wireEnterFocus(itemBarcodeInput, qtyInput);
  wireEnterFocus(qtyInput, caseSizeInput);
  wireEnterFocus(caseSizeInput, batchInput);
  wireEnterFocus(batchInput, expiryInput);

  const receiptForm = document.querySelector("form[action^='/tasker/api/pallets/'][enctype='multipart/form-data']");
  if (receiptForm && unknownSkuInput) {
    receiptForm.addEventListener("submit", function(event) {
      if (unknownSkuInput.value !== "1") return;
      const photosInput = document.getElementById("stock_photos");
      const hasPhoto = photosInput && photosInput.files && photosInput.files.length > 0;
      if (hasPhoto) return;
      event.preventDefault();
      if (unknownSkuHint) unknownSkuHint.classList.remove("hidden");
      if (typeof openPhotoModal === "function") {
        openPhotoModal();
      }
    });
  }

  function applyLineEditorData(trigger) {
    if (!trigger || !lineEditorForm || !lineDeleteForm || !lineEditorModal) return;
    const palletID = String(trigger.getAttribute("data-pallet-id") || "").trim();
    const receiptID = String(trigger.getAttribute("data-receipt-id") || "").trim();
    if (!palletID || !receiptID) return;

    lineEditorForm.action = "/tasker/api/pallets/" + encodeURIComponent(palletID) + "/receipts/" + encodeURIComponent(receiptID) + "/update";
    lineDeleteForm.action = "/tasker/api/pallets/" + encodeURIComponent(palletID) + "/receipts/" + encodeURIComponent(receiptID) + "/delete";

    const sku = document.getElementById("line_edit_sku");
    const description = document.getElementById("line_edit_description");
    const uom = document.getElementById("line_edit_uom");
    const comment = document.getElementById("line_edit_comment");
    const qty = document.getElementById("line_edit_qty");
    const caseSize = document.getElementById("line_edit_case_size");
    const batch = document.getElementById("line_edit_batch");
    const expiry = document.getElementById("line_edit_expiry");
    const damaged = document.getElementById("line_edit_damaged");

    if (sku) sku.value = String(trigger.getAttribute("data-sku") || "");
    if (description) description.value = String(trigger.getAttribute("data-description") || "");
    if (uom) uom.value = String(trigger.getAttribute("data-uom") || "");
    if (comment) comment.value = String(trigger.getAttribute("data-comment") || "");
    if (qty) qty.value = String(trigger.getAttribute("data-qty") || "");
    if (caseSize) caseSize.value = String(trigger.getAttribute("data-case-size") || "");
    if (batch) batch.value = String(trigger.getAttribute("data-batch") || "");
    if (expiry) expiry.value = String(trigger.getAttribute("data-expiry") || "");
    if (damaged) damaged.checked = String(trigger.getAttribute("data-damaged") || "0") === "1";

    lineEditorModal.showModal();
  }

  document.querySelectorAll("[data-line-edit-trigger='1']").forEach(function(trigger) {
    trigger.addEventListener("click", function(event) {
      if (event.target.closest("a, button, input, select, textarea, form, label")) {
        return;
      }
      applyLineEditorData(trigger);
    });
  });
})();
</script>

<dialog id="comment-modal" class="modal">
  <div class="modal-box max-w-lg">
    <h3 class="text-lg font-semibold">Receipt Comment</h3>
    <p class="mt-1 text-sm text-base-content/60">Optional note for this line item.</p>
    <textarea id="comment_modal_text" class="textarea textarea-bordered w-full mt-3 min-h-32" placeholder="Enter comment"></textarea>
    <div class="modal-action flex-col sm:flex-row gap-2">
      <button class="btn btn-primary w-full sm:flex-1" type="button" onclick="saveCommentValue()">Save Comment</button>
      <button class="btn btn-ghost w-full sm:flex-1" type="button" onclick="closeCommentModal()">Cancel</button>
    </div>
  </div>
  <form method="dialog" class="modal-backdrop"><button type="submit">close</button></form>
</dialog>

<dialog id="photo-modal" class="modal">
  <div class="modal-box max-w-3xl">
    <h3 class="text-lg font-semibold">Take Stock Photos</h3>
    <div class="mt-3 relative">
      <video id="photo-video" class="w-full rounded-lg bg-neutral" autoplay playsinline muted></video>
      <canvas id="photo-canvas" class="hidden"></canvas>
      <img id="photo-preview" class="hidden w-full rounded-lg" alt="Captured photo" />
    </div>
    <p id="photo-modal-status" class="mt-3 text-sm text-base-content/60">Camera idle</p>
    <div id="photo-modal-thumbs" class="flex gap-2 mt-3 overflow-x-auto pb-1"></div>
    <div class="modal-action flex-col sm:flex-row gap-2">
      <button id="photo-capture-btn" class="btn btn-primary btn-lg w-full sm:flex-1" type="button" onclick="capturePhoto()">
        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" class="size-5"><circle cx="12" cy="12" r="9"/></svg>
        Take Photo
      </button>
      <button id="photo-retake-btn" class="btn btn-outline btn-lg w-full sm:flex-1 hidden" type="button" onclick="retakePhoto()">Retake</button>
      <button id="photo-add-btn" class="btn btn-success btn-lg w-full sm:flex-1 hidden" type="button" onclick="addPhotoAndContinue()">Add &amp; Take Another</button>
      <button id="photo-done-btn" class="btn btn-primary btn-lg w-full sm:flex-1 hidden" type="button" onclick="addPhotoAndClose()">Add &amp; Done</button>
      <button class="btn btn-ghost btn-lg w-full sm:flex-1" type="button" onclick="closePhotoModal()">Dismiss</button>
    </div>
  </div>
</dialog>

<script>
let photoStream = null;
let capturedPhotos = [];

function setPhotoStatus(msg) {
  const el = document.getElementById("photo-modal-status");
  if (el) el.textContent = msg;
}

function renderPhotoThumbs(container) {
  if (!container) container = document.getElementById("photo-modal-thumbs");
  if (!container) return;
  container.innerHTML = "";
  capturedPhotos.forEach(function(p, i) {
    const wrap = document.createElement("div");
    wrap.className = "relative shrink-0";
    const img = document.createElement("img");
    img.src = p.dataURL;
    img.className = "w-16 h-16 rounded-lg object-cover border border-base-300";
    img.alt = "Photo " + (i + 1);
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn btn-circle btn-xs btn-error absolute -top-2 -right-2";
    btn.innerHTML = "&times;";
    btn.onclick = function(e) { e.preventDefault(); removePhoto(i); };
    wrap.appendChild(img);
    wrap.appendChild(btn);
    container.appendChild(wrap);
  });
}

function renderFormThumbs() {
  const container = document.getElementById("photo-thumbs");
  if (!container) return;
  container.innerHTML = "";
  capturedPhotos.forEach(function(p, i) {
    const wrap = document.createElement("div");
    wrap.className = "relative shrink-0";
    const img = document.createElement("img");
    img.src = p.dataURL;
    img.className = "w-20 h-20 rounded-lg object-cover border border-base-300 shadow-sm";
    img.alt = "Photo " + (i + 1);
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn btn-circle btn-xs btn-error absolute -top-2 -right-2";
    btn.innerHTML = "&times;";
    btn.onclick = function(e) { e.preventDefault(); removePhoto(i); };
    wrap.appendChild(img);
    wrap.appendChild(btn);
    container.appendChild(wrap);
  });
}

function removePhoto(index) {
  capturedPhotos.splice(index, 1);
  syncPhotosToInput();
  renderPhotoThumbs();
  renderFormThumbs();
  updatePhotoStatus();
}

function updatePhotoStatus() {
  const status = document.getElementById("photo-status");
  if (!status) return;
  const n = capturedPhotos.length;
  if (n === 0) {
    status.textContent = "No photos";
    status.className = "text-sm text-base-content/60";
  } else {
    status.textContent = n + " photo" + (n > 1 ? "s" : "") + " attached";
    status.className = "text-sm text-success font-medium";
  }
}

function syncPhotosToInput() {
  const dt = new DataTransfer();
  capturedPhotos.forEach(function(p, i) {
    dt.items.add(new File([p.blob], "stock_photo_" + (i + 1) + ".jpg", { type: "image/jpeg" }));
  });
  const input = document.getElementById("stock_photos");
  if (input) input.files = dt.files;
}

async function openPhotoModal() {
  const modal = document.getElementById("photo-modal");
  if (!modal) return;
  modal.showModal();
  resetPhotoUI();
  renderPhotoThumbs();
  setPhotoStatus("Starting camera...");
  try {
    const video = document.getElementById("photo-video");
    photoStream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 } },
      audio: false
    });
    video.srcObject = photoStream;
    await video.play();
    setPhotoStatus(capturedPhotos.length > 0 ? capturedPhotos.length + " photo(s) so far. Position item and tap Take Photo" : "Position item and tap Take Photo");
  } catch (err) {
    setPhotoStatus("Camera failed: " + (err && err.message ? err.message : err));
  }
}

function capturePhoto() {
  const video = document.getElementById("photo-video");
  const canvas = document.getElementById("photo-canvas");
  const preview = document.getElementById("photo-preview");
  if (!video || !canvas || !preview) return;

  canvas.width = video.videoWidth;
  canvas.height = video.videoHeight;
  const ctx = canvas.getContext("2d");
  ctx.drawImage(video, 0, 0);

  preview.src = canvas.toDataURL("image/jpeg", 0.85);
  video.classList.add("hidden");
  preview.classList.remove("hidden");

  document.getElementById("photo-capture-btn").classList.add("hidden");
  document.getElementById("photo-retake-btn").classList.remove("hidden");
  document.getElementById("photo-add-btn").classList.remove("hidden");
  document.getElementById("photo-done-btn").classList.remove("hidden");
  setPhotoStatus("Photo captured. Add it or retake.");
}

function retakePhoto() {
  const video = document.getElementById("photo-video");
  const preview = document.getElementById("photo-preview");
  video.classList.remove("hidden");
  preview.classList.add("hidden");

  document.getElementById("photo-capture-btn").classList.remove("hidden");
  document.getElementById("photo-retake-btn").classList.add("hidden");
  document.getElementById("photo-add-btn").classList.add("hidden");
  document.getElementById("photo-done-btn").classList.add("hidden");
  setPhotoStatus("Position item and tap Take Photo");
}

function addCurrentPhoto(callback) {
  const canvas = document.getElementById("photo-canvas");
  if (!canvas) return;
  canvas.toBlob(function(blob) {
    if (!blob) return;
    const dataURL = canvas.toDataURL("image/jpeg", 0.85);
    capturedPhotos.push({ blob: blob, dataURL: dataURL });
    syncPhotosToInput();
    renderPhotoThumbs();
    renderFormThumbs();
    updatePhotoStatus();
    if (callback) callback();
  }, "image/jpeg", 0.85);
}

function addPhotoAndContinue() {
  addCurrentPhoto(function() {
    resetPhotoUI();
    renderPhotoThumbs();
    setPhotoStatus(capturedPhotos.length + " photo(s) taken. Take another or press Dismiss.");
  });
}

function addPhotoAndClose() {
  addCurrentPhoto(function() {
    closePhotoModal();
  });
}

function resetPhotoUI() {
  const video = document.getElementById("photo-video");
  const preview = document.getElementById("photo-preview");
  if (video) video.classList.remove("hidden");
  if (preview) preview.classList.add("hidden");
  document.getElementById("photo-capture-btn").classList.remove("hidden");
  document.getElementById("photo-retake-btn").classList.add("hidden");
  document.getElementById("photo-add-btn").classList.add("hidden");
  document.getElementById("photo-done-btn").classList.add("hidden");
}

function closePhotoModal() {
  if (photoStream) {
    photoStream.getTracks().forEach(function(t) { t.stop(); });
    photoStream = null;
  }
  const video = document.getElementById("photo-video");
  if (video) video.srcObject = null;
  const modal = document.getElementById("photo-modal");
  if (modal && modal.open) modal.close();
  updatePhotoStatus();
}
</script>`
}
