# Third-party notices

- `jinqians/snell.sh`: GPL-3.0。仅作为行为兼容参考；本仓库不复制或执行其远程脚本。
- `jinqians/ss-2022.sh`: MIT。仅作为行为兼容参考。
- Snell server: Surge 官方发布。再分发条件未确认，不包含在本仓库或镜像中。
- `shadowsocks/shadowsocks-rust`: MIT；运行时从官方 GitHub Release 下载。
- `ihciah/shadow-tls`: MIT；运行时从官方 GitHub Release 下载。
- `openconnect/ocserv`: GPL-2.0-or-later；镜像构建时从官方 1.5.0 发布包编译并校验固定 SHA-256，对应源码包同时保存在镜像 `/usr/share/source/ocserv-1.5.0.tar.xz`。上游位于 <https://gitlab.com/openconnect/ocserv>。
- `MoeClub/ocserv_docker`: GPL-3.0；作为容器部署与配置语义参考。
- Vue、Vite、Go modules 和 Debian 软件包分别适用其上游许可证。发布镜像前应由 CI 生成完整 SBOM 和许可证清单。
