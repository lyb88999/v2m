#!/bin/sh
set -eu

URL="${1:-http://localhost:8080/jobs?limit=1}"
COUNT="${2:-40}"
TOKEN="${API_TOKEN:-}"

AUTH_HEADER=""
if [ -n "$TOKEN" ]; then
  AUTH_HEADER="Authorization: Bearer $TOKEN"
fi

echo "URL: $URL"
echo "COUNT: $COUNT"
if [ -n "$AUTH_HEADER" ]; then
  echo "TOKEN: set"
else
  echo "TOKEN: empty"
fi

i=1
rate_limited=0
while [ "$i" -le "$COUNT" ]; do
  if [ -n "$AUTH_HEADER" ]; then
    code="$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" "$URL")"
  else
    code="$(curl -s -o /dev/null -w "%{http_code}" "$URL")"
  fi
  printf "%s " "$code"
  if [ "$code" = "429" ]; then
    rate_limited=$((rate_limited + 1))
  fi
  i=$((i + 1))
done
echo
echo "429 count: $rate_limited"
