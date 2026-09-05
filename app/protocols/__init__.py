"""天翼云无浏览器协议客户端。"""

from .common import ProtocolError

__all__ = ["ProtocolError", "CtYunProtocolClient", "EaiProtocolClient"]


def __getattr__(name: str):
    # 两套协议按需加载，积分任务不会因为 AI 加密库初始化失败而受影响。
    if name == "CtYunProtocolClient":
        from .ctyun import CtYunProtocolClient

        return CtYunProtocolClient
    if name == "EaiProtocolClient":
        from .eai import EaiProtocolClient

        return EaiProtocolClient
    raise AttributeError(name)

__all__ = ["CtYunProtocolClient", "EaiProtocolClient", "ProtocolError"]
