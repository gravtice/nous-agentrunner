import asyncio

from .server import run


def main() -> None:
    asyncio.run(run())

