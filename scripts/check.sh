#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

fail() { printf "${RED}FAIL${NC} %s\n" "$*"; }
pass() { printf "${GREEN}PASS${NC} %s\n" "$*"; }

echo "=== services ==="
for port in 8180 8181 8182 8183; do
  if   [ "$port" = 8180 ]; then svc=gateway
  elif [ "$port" = 8181 ]; then svc=catalog
  elif [ "$port" = 8182 ]; then svc=recsys
  elif [ "$port" = 8183 ]; then svc=ingest
  fi
  if lsof -i :${port} 2>/dev/null | grep -q LISTEN; then
    pass "$svc :${port}"
  else
    fail "$svc :${port} NOT RUNNING"
  fi
done

echo ""
echo "=== gateway MEDIA_ROOT ==="
pid=$(lsof -i :8180 2>/dev/null | grep LISTEN | awk '{print $2}' | head -1)
if [ -z "$pid" ]; then
  fail "gateway not running, cannot check MEDIA_ROOT"
else
  media=$(ps eww -p "$pid" 2>/dev/null | tr ' ' '\n' | grep '^MEDIA_ROOT=' | cut -d= -f2)
  if [ -z "$media" ]; then
    fail "MEDIA_ROOT not set (defaults to ./media)"
  elif [ "$media" = "/Volumes/Data2/Youtube" ]; then
    pass "MEDIA_ROOT=$media"
  else
    fail "MEDIA_ROOT=$media (expected /Volumes/Data2/Youtube)"
  fi
fi

echo ""
echo "=== ingest MEDIA_ROOT ==="
ipid=$(lsof -i :8183 2>/dev/null | grep LISTEN | awk '{print $2}' | head -1)
if [ -z "$ipid" ]; then
  fail "ingest not running, cannot check MEDIA_ROOT"
else
  imedia=$(ps eww -p "$ipid" 2>/dev/null | tr ' ' '\n' | grep '^MEDIA_ROOT=' | cut -d= -f2)
  if [ -z "$imedia" ]; then
    fail "MEDIA_ROOT not set (defaults to ./media — files go to wrong place!)"
  elif [ "$imedia" = "/Volumes/Data2/Youtube" ]; then
    pass "MEDIA_ROOT=$imedia"
  else
    fail "MEDIA_ROOT=$imedia (expected /Volumes/Data2/Youtube)"
  fi
fi

echo ""
echo "=== media serving ==="
test_file=$(ls /Volumes/Data2/Youtube/*/1080p.mp4 2>/dev/null | head -1)
if [ -z "$test_file" ]; then
  echo "no 1080p.mp4 files found to test"
else
  rel="${test_file#/Volumes/Data2/Youtube/}"
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:8180/media/$rel" 2>/dev/null)
  if [ "$code" = "200" ]; then
    pass "/media/$rel -> 200"
  else
    fail "/media/$rel -> $code"
  fi
fi

echo ""
echo "=== feed ==="
count=$(curl -s "http://localhost:8180/api/feed?pageSize=6" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('videos',[])))" 2>/dev/null)
if [ "$count" -gt 0 ] 2>/dev/null; then
  pass "feed returns $count videos"
else
  fail "feed returns $count videos"
fi

echo ""
echo "=== ingest ==="
jobs_count=$(curl -s "http://localhost:8180/api/ingest/jobs" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('jobs',[])))" 2>/dev/null)
if [ -n "$jobs_count" ]; then
  pass "ingest/jobs returns $jobs_count entries"
else
  fail "ingest/jobs unreachable"
fi

echo ""
echo "=== postgres ==="
if pg_isready -q 2>/dev/null; then
  pass "postgres accepting connections"
else
  fail "postgres NOT reachable"
fi
