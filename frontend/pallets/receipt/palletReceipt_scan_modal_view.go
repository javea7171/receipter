package receipt

func renderScanModalAssets() string {
	return `<dialog id="scan-modal" class="modal">
  <div class="modal-box max-w-3xl">
    <h3 class="text-lg font-semibold">Scan Barcode</h3>
    <div id="scan-reader" class="mt-3 h-72 w-full overflow-hidden rounded-lg bg-neutral text-neutral-content"></div>
    <p id="scan-status" class="mt-3 text-sm opacity-70">Camera idle</p>
    <div class="modal-action">
      <button class="btn" type="button" onclick="closeScanModal()">Close</button>
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
})();
</script>`
}
