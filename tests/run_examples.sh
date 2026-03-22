#!/bin/bash
# Run all 50 Codong example scripts and report results
# Usage: ./tests/run_examples.sh [path-to-codong-binary]

CODONG=${1:-"./bin/codong"}
PASS=0; FAIL=0
FAILURES=()

echo "Running all Codong examples..."
echo ""

for f in examples/*.cod; do
    name=$(basename "$f")
    out=$("$CODONG" eval "$f" 2>&1)
    rc=$?
    # Fail if exit code non-zero or output contains runtime error codes
    if [ $rc -ne 0 ] || echo "$out" | grep -q '"code":"E[0-9]'; then
        FAIL=$((FAIL+1))
        FAILURES+=("❌  $name")
    else
        PASS=$((PASS+1))
        echo "✅  $name"
    fi
done

echo ""
echo "══════════════════════════════════════════"
if [ ${#FAILURES[@]} -gt 0 ]; then
    for f in "${FAILURES[@]}"; do echo "$f"; done
    echo ""
fi
echo "  ✅ PASS: $PASS   ❌ FAIL: $FAIL   总计: $((PASS+FAIL))"
echo "══════════════════════════════════════════"
exit $FAIL
