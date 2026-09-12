CtYunKeeper Windows 使用说明
============================

1. 保留压缩包中的完整目录结构，不要只复制 ctyun-keeper.exe。
2. 根据需要编辑 config.env；默认 Web 端口为 9845。
3. 双击 start.bat 启动程序。
4. 浏览器访问 http://127.0.0.1:9845。
5. 账号、密钥、数据库和日志会保存在 data 目录中。

首次进入管理页面时，需要设置至少 8 位的管理密码。
关闭命令窗口会停止程序。

在线更新
--------
- 使用 start.bat 启动时保持 UPDATE_RESTART_MODE=self，更新助手会自动重启程序。
- 使用 NSSM 或其他服务管理器时改为 supervisor，并将服务失败重启延迟设置为至少 10 秒。
- 在线更新不会覆盖 config.env、start.bat 和 data。
- 新版本健康检查失败时会恢复上一版本程序和更新前数据库。


服务模式在线更新：
UPDATE_RESTART_MODE=auto 自动识别服务；也可指定 supervisor 和 UPDATE_SERVICE_NAME。
运行账户需有该服务的查询、停止和启动权限。NSSM 用户先执行：
nssm set <服务名> AppKillProcessTree 0
否则更新助手会拒绝更新，避免与服务自动重启竞争。
更新完成或失败后，系统设置显示最终结果；回滚会同步恢复更新前数据库。
