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

验收脚本同时检查：本机 HTTP 健康、基础 schema、持久化
`CLICKHOUSE_REQUIRED=true`，以及应用账号的临时表写入和查询冒烟。临时表会在验收结束时清理，密码不会出现在输出中。

默认端点仅监听本机：HTTP `127.0.0.1:8123`，Native `127.0.0.1:9000`。数据库名为 `onchain`，应用用户为 `etl_app`。

登录启动项（注册表 Run 项 `ClickHouseBSC`）通过同目录的 `start-clickhouse-hidden.vbs` 以完全隐藏窗口的方式运行 `start-clickhouse.ps1`（若直接写 `powershell -WindowStyle Hidden`，登录时会闪一下终端窗口；只有 VBS 的 `shell.Run ..., 0, False` 才能真正不显示），并保留一个隐藏的 `clickhouse-wsl-keeper` 进程，避免 WSL 在启动命令退出后回收发行版。若端口意外消失，可重新运行该启动脚本恢复。
