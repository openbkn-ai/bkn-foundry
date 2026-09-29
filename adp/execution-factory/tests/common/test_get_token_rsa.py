# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Offline checks for GetToken.auth_Pwd_RSABase64.

The server decrypts the password with RSAES-PKCS1-v1_5 (what M2Crypto's
``RSA.pkcs1_padding`` produced). These tests pin that scheme without a live
server: encrypt with the helper, decrypt with the matching private key.
"""

import base64

import pytest
from Crypto.Cipher import PKCS1_v1_5
from Crypto.PublicKey import RSA

from common.get_token import GetToken


def _wrap_like_embedded(pem):
    # GetToken.key / key1 are triple-quoted with a leading newline and
    # trailing indentation; the helper must accept that shape.
    return "\n" + pem + "\n        "


def _decrypt(private_key, b64):
    sentinel = object()
    plain = PKCS1_v1_5.new(private_key).decrypt(base64.b64decode(b64), sentinel)
    assert plain is not sentinel, "ciphertext is not valid PKCS#1 v1.5"
    return plain


@pytest.mark.parametrize("bits", [1024, 2048])
@pytest.mark.parametrize("message", ["111111", "eisoo.com123", "p@ss w\u00f6rd \u00e9"])
def test_round_trip_pkcs1_v1_5(bits, message):
    private_key = RSA.generate(bits)
    pem = private_key.publickey().export_key(format="PEM").decode("ascii")

    result = GetToken("127.0.0.1").auth_Pwd_RSABase64(_wrap_like_embedded(pem), message)

    assert isinstance(result, str)
    raw = base64.b64decode(result, validate=True)
    assert len(raw) == bits // 8
    assert _decrypt(private_key, result) == message.encode("utf8")


def test_padding_is_randomized():
    private_key = RSA.generate(1024)
    pem = private_key.publickey().export_key(format="PEM").decode("ascii")
    client = GetToken("127.0.0.1")

    first = client.auth_Pwd_RSABase64(pem, "111111")
    second = client.auth_Pwd_RSABase64(pem, "111111")

    assert first != second
    assert _decrypt(private_key, first) == _decrypt(private_key, second) == b"111111"


@pytest.mark.parametrize("attr, bits", [("key", 2048), ("key1", 1024)])
def test_embedded_public_keys_still_load(attr, bits):
    client = GetToken("127.0.0.1")

    result = client.auth_Pwd_RSABase64(getattr(client, attr), "111111")

    assert len(base64.b64decode(result, validate=True)) == bits // 8


def test_message_too_long_is_rejected():
    private_key = RSA.generate(1024)
    pem = private_key.publickey().export_key(format="PEM").decode("ascii")

    # PKCS#1 v1.5 allows at most k - 11 bytes of plaintext.
    with pytest.raises(ValueError):
        GetToken("127.0.0.1").auth_Pwd_RSABase64(pem, "x" * (private_key.size_in_bytes() - 10))
