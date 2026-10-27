#!/bin/sh
# 生成内部后台Token：明文只发给使用人，SHA-256 写进 config.yaml 的 admin.tokens。
set -eu
t=$(openssl rand -hex 32)
echo "token(只发给使用人):           $t"
echo "token_sha256(写进config.yaml): $(printf %s "$t" | openssl dgst -sha256 | awk '{print $NF}')"
