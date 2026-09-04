import os
import secrets
from pathlib import Path

from argon2 import PasswordHasher
from argon2.exceptions import InvalidHashError, VerifyMismatchError
from cryptography.fernet import Fernet

from .config import FERNET_KEY_FILE, SESSION_KEY_FILE, ensure_directories


_password_hasher = PasswordHasher()


def _load_or_create(path: Path, factory) -> bytes:
    ensure_directories()
    if path.exists():
        return path.read_bytes().strip()
    value = factory()
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    try:
        descriptor = os.open(path, flags, 0o600)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(value + b"\n")
    except FileExistsError:
        return path.read_bytes().strip()
    return value


def session_secret() -> str:
    return _load_or_create(
        SESSION_KEY_FILE, lambda: secrets.token_urlsafe(48).encode("ascii")
    ).decode("ascii")


def _fernet() -> Fernet:
    return Fernet(_load_or_create(FERNET_KEY_FILE, Fernet.generate_key))


def encrypt_secret(value: str) -> str:
    return _fernet().encrypt(value.encode("utf-8")).decode("ascii")


def decrypt_secret(value: str) -> str:
    return _fernet().decrypt(value.encode("ascii")).decode("utf-8")


def hash_password(password: str) -> str:
    return _password_hasher.hash(password)


def verify_password(encoded: str, password: str) -> bool:
    try:
        return _password_hasher.verify(encoded, password)
    except (VerifyMismatchError, InvalidHashError):
        return False


def new_csrf_token() -> str:
    return secrets.token_urlsafe(32)
