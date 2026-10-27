# 部署说明(Docker，Linux amd64)

## 包里有什么

| 文件                                         | 说明                                                     |
| -------------------------------------------- | -------------------------------------------------------- |
| `afu-server-<版本>-linux-amd64.image.tar.gz` | 服务镜像(前端已内置)，用 `docker load` 加载              |
| `docker-compose.yml`                         | afu 服务 + MySQL 8.4                                     |
| `.env.example`                               | 版本号、数据库密码、端口                                 |
| `config.yaml.example`                        | 服务配置模板                                             |
| `migrations/*.sql`                           | 建表SQL，MySQL首次启动(数据卷为空)时按文件名顺序自动执行 |
| `gen-keys.sh`                                | 生成rsa密钥                                              |
| `gen-admin-token.sh`                         | 生成内部后台Token                                        |
| `nginx.conf.example`                         | HTTPS反向代理示例                                        |

## 前提

1. Linux amd64，已安装 Docker 和 Compose v2(`docker compose version` 能输出版本)。
2. 服务器上有 openssl。
3. 域名和HTTPS证书：SDK必须走HTTPS(iOS ATS、Android 9+ 默认禁止明文HTTP)。

## 首次部署

固定部署在 `/opt/afu`，以后升级也在这个目录里操作。

1. 上传安装包并解压到 `/opt/afu`：

   ```bash
   sudo mkdir -p /opt/afu
   sudo tar -xzf afu-server-<版本>-linux-amd64.tar.gz -C /opt/afu --strip-components=1
   cd /opt/afu
   ```

2. 加载镜像：

   ```bash
   gunzip -c afu-server-*-linux-amd64.image.tar.gz | sudo docker load
   ```

3. 环境变量：

   ```bash
   sudo cp .env.example .env && sudo chmod 600 .env
   openssl rand -hex 16   # 生成密码，执行两次，分别给 MYSQL_ROOT_PASSWORD、MYSQL_PASSWORD
   ```

   编辑 `.env`：`AFU_VERSION` 已填好；填两个密码；`AFU_DB_DSN` 里的密码要和 `MYSQL_PASSWORD` 一致。密码只用字母和数字，`/`、`@` 会破坏DSN。

4. 密钥：

   ```bash
   sudo ./gen-keys.sh
   ```

   1. 私钥 `keys/kid1.pem` 只留在服务器；把 `keys/kid1.pub.pem`(Android)、`keys/kid1.pkcs1.pub.pem`(iOS)发给客户端内置。
   2. 如果客户端已经内置了某个公钥，就把对应的私钥放到 `keys/kid1.pem`，不要重新生成，否则客户端请求全部 decrypt_failed。

5. 服务配置：

   ```bash
   sudo cp config.yaml.example config.yaml
   ./gen-admin-token.sh   # 每个使用人执行一次
   ```

   把每个人的 name 和 token_sha256 填进 `config.yaml` 的 `admin.tokens`，Token明文只发给本人。然后：

   ```bash
   sudo chown 10001:10001 config.yaml && sudo chmod 600 config.yaml   # 容器内以uid 10001运行
   ```

6. 启动：

   ```bash
   sudo docker compose up -d
   sudo docker compose ps          # mysql 显示 healthy，afu 显示 running
   sudo docker compose logs afu    # 能看到 listening :8080 和 :18081
   ```

7. 内部后台：在自己电脑上开SSH隧道，浏览器打开 http://127.0.0.1:18081 ，用第5步的Token登录。

   ```bash
   ssh -L 18081:127.0.0.1:18081 <用户>@<服务器>
   ```

8. App Key：在后台「App Key」页点「生成 App Key」，把生成的值发给客户。已经发给客户的 App Key 填在「导入已有的 App Key」里。没有已启用的 App Key 时，所有注册、上报都返回403。
9. 冒烟测试(会在库里留下几条测试数据，计入统计)：

   ```bash
   sudo docker compose exec afu /app/fakeclient -url http://127.0.0.1:8080/afu/v1/meter \
     -pub /app/keys/kid1.pub.pem -app_key <第8步的 App Key>
   ```

   各步都打印出 ack / refused 即正常。

10. HTTPS：参照 `nginx.conf.example` 配好nginx，客户端地址为 `https://<域名>/afu/v1/meter`。

## 升级

1. 新安装包解压到临时目录，`docker load` 加载新镜像。
2. 新包里的 `docker-compose.yml`、`migrations/` 有变化时，复制到 `/opt/afu` 覆盖；`.env`、`config.yaml`、`keys/` 不要覆盖。
3. 改 `/opt/afu/.env` 里的 `AFU_VERSION` 为新版本。
4. 有新的迁移文件时，先在数据库里执行(已有数据的库不会自动执行)：

   ```bash
   sudo docker compose exec -T mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" afu' < migrations/002_app_keys.sql
   ```

   从「app_key 写在配置里」的版本升级时：先执行 `002_app_keys.sql`，升级后马上在后台把原来配置里的 app_key 导入，否则客户端请求会返回403。
5. 只重建服务，数据库不动：

   ```bash
   cd /opt/afu && sudo docker compose up -d afu
   ```

回滚：把 `AFU_VERSION` 改回旧版本，再执行第5步。

## 以后换云数据库

1. 云数据库(MySQL 8 兼容)上建库 `afu` 和账号，按文件名顺序执行 `migrations/` 下的全部SQL。
2. 迁移旧数据：

   ```bash
   sudo docker compose exec mysql sh -c 'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction afu' > afu.sql
   ```

   再导入云数据库。
3. 改 `.env` 的 `AFU_DB_DSN` 指向云数据库；需要TLS时在DSN末尾加 `&tls=true`(镜像里已带CA证书)。
4. 在 `docker-compose.yml` 里删掉 `mysql` 服务、`afu` 的 `depends_on`、最后的 `volumes`。
5. `sudo docker compose up -d afu --remove-orphans`。
6. 建议给内部后台单独建只读账号，填进 `config.yaml` 的 `admin.db_dsn`。

## 日常运维

| 操作         | 命令                                                                            |
| ------------ | ------------------------------------------------------------------------------- |
| 看日志       | `sudo docker compose logs -f --tail 200 afu`                                    |
| 重启服务     | `sudo docker compose restart afu`                                               |
| 停止全部     | `sudo docker compose down`(数据在卷里，不会丢；不要加 `-v`)                     |
| 备份数据库   | 见下方命令                                                                      |
| 新增后台用户 | `./gen-admin-token.sh`，填进 `config.yaml` 后 `sudo docker compose restart afu` |

备份数据库：

```bash
sudo docker compose exec mysql sh -c 'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction afu' | gzip > afu-$(date +%F).sql.gz
```

日志是JSON格式，Docker按单文件50MB、最多5个文件自动轮转。

## 安全检查

1. 内部后台端口只绑 `127.0.0.1`(compose已配置)，不要在nginx里转发它。
2. SDK端口默认也只绑 `127.0.0.1`，由同机nginx转发；用云负载均衡时才把 `AFU_HTTP_BIND` 改成 `0.0.0.0`，并在安全组里只放行负载均衡。
3. `keys/kid1.pem`、`config.yaml`、`.env` 权限都是600，不要提交到任何仓库。
4. MySQL不对宿主机暴露端口；定期备份。
