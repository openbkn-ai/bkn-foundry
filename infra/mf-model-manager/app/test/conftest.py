"""Shared test setup for mf-model-manager."""
import os

# The service now defaults to AUTH_ENABLED=true so a deployment that forgets the
# variable fails closed. The suite predates that default and asserts the
# anonymous-allow behaviour, so pin the open value before project code is
# imported; cases that exercise the auth path patch base_config themselves.
os.environ.setdefault("AUTH_ENABLED", "false")
