#!/usr/bin/env sh
set -eu

OUT_DIR="${1:-./data/lmfpd}"
BASE_URL="${LMFPD_BASE_URL:-https://raw.githubusercontent.com/haowang02/cpa-plugin-codex-candy-eval/main/internal/fingerprint/data}"

mkdir -p "$OUT_DIR"
for f in shared_detector.bin models.json enrollment-suite.json LICENSE; do
  echo "Downloading ${BASE_URL}/${f} -> ${OUT_DIR}/${f}"
  curl -fsSL "${BASE_URL}/${f}" -o "${OUT_DIR}/${f}"
done
echo "Done. External lmfpd reference bank updated at ${OUT_DIR}"
