#!/bin/sh
# 生成 kid1 的rsa密钥：私钥留在服务器，公钥发给客户端(Android用 SPKI，iOS用 PKCS#1)。
# 已有私钥时不覆盖：重新生成会让已发给客户端的公钥失效。
set -eu
cd "$(dirname "$0")"
if [ -e keys/kid1.pem ]; then
  echo "keys/kid1.pem 已存在，不覆盖"
  exit 1
fi
mkdir -p keys
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out keys/kid1.pem 2>/dev/null
openssl pkey -in keys/kid1.pem -pubout -out keys/kid1.pub.pem
openssl rsa -in keys/kid1.pem -RSAPublicKey_out -out keys/kid1.pkcs1.pub.pem 2>/dev/null
chmod 600 keys/kid1.pem
# 容器内以 uid 10001 运行，需要能读私钥
if [ "$(id -u)" = "0" ]; then
  chown -R 10001:10001 keys
else
  echo "提示：用 root 执行 chown -R 10001:10001 keys，否则容器读不到私钥"
fi
echo "已生成："
echo "  keys/kid1.pem            私钥，只留在服务器"
echo "  keys/kid1.pub.pem        Android 公钥(SubjectPublicKeyInfo)"
echo "  keys/kid1.pkcs1.pub.pem  iOS 公钥(PKCS#1)"
