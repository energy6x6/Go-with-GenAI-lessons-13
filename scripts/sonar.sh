#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT_DIR="$PWD"
: "${SONAR_TOKEN:?Export a project analysis token first}"
: "${SONAR_EVIDENCE_DIR:?Set an absolute evidence directory outside the repository}"
SONAR_EVIDENCE_DIR="$(python3 - "$ROOT_DIR" "$SONAR_EVIDENCE_DIR" <<'PY'
from pathlib import Path
import sys
root, evidence = map(lambda s: Path(s).resolve(), sys.argv[1:])
if root == evidence or root in evidence.parents:
    raise SystemExit('SonarQube evidence must stay outside the repository')
if not (evidence / 'coverage.out').is_file():
    raise SystemExit('Merged coverage.out is missing')
print(evidence)
PY
)"
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --network lesson13-quality_default \
  -e SONAR_TOKEN -e SONAR_USER_HOME=/tmp/sonar-user \
  -v "$ROOT_DIR:/usr/src:ro" -v "$SONAR_EVIDENCE_DIR:/evidence" \
  sonarsource/sonar-scanner-cli@sha256:47956b669bf486bdad919e1cbd0533223515aa0b6d1d31763e5a3754942df6d7 \
  -Dsonar.host.url=http://sonarqube:9000 \
  -Dsonar.working.directory=/evidence/scannerwork \
  -Dsonar.go.coverage.reportPaths=/evidence/coverage.out \
  "-Dsonar.projectVersion=$(git rev-parse --short HEAD)"
