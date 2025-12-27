#!/bin/bash
cd /workspaces/smux
echo "Running all tests..."
go test -v . 2>&1 | tee test_output.txt
echo ""
echo "Test Summary:"
grep -E "PASS|FAIL" test_output.txt | tail -20
