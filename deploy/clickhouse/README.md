# ClickHouse E 盘部署

本目录用于把 ClickHouse 部署到专用 WSL2 发行版 `clickhouse-bsc`。发行版 VHD、ClickHouse 数据、日志和临时文件的物理存储均位于 `E:\database\clickhouse`。

执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\clickhouse\deploy-clickhouse.ps1
```

若 Windows 刚启用 WSL2/虚拟机平台，脚本会请求管理员权限并登记一次性续跑项。重启并登录后会自动继续安装。数据库凭据写入 `E:\database\clickhouse\config\clickhouse.env`，ACL 仅授予当前用户读写权限。

验收：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\deploy\clickhouse\verify-clickhouse.ps1
```

默认端点仅监听本机：HTTP `127.0.0.1:8123`，Native `127.0.0.1:9000`。数据库名为 `onchain`，应用用户为 `etl_app`。

登录启动项会运行 `start-clickhouse.ps1`，并保留一个隐藏的 `clickhouse-wsl-keeper` 进程，避免 WSL 在启动命令退出后回收发行版。若端口意外消失，可重新运行该启动脚本恢复。
