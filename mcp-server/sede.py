from typing import Any
import httpx
from mcp.server.fastmcp import FastMCP

# Initialize FastMCP server
mcp = FastMCP("sede")

SEDE_API_STATUS = "https://sede.olografix.org/status"
USER_AGENT = "sede-mcp-server/1.0"


async def make_sede_request(url: str) -> dict[str, Any] | None:
    """Make a request to the Olografix HQ API with proper error handling."""
    headers = {"User-Agent": USER_AGENT, "Accept": "application/text"}
    async with httpx.AsyncClient() as client:
        try:
            response = await client.get(url, headers=headers, timeout=30.0)
            response.raise_for_status()
            return response.text
        except Exception:
            return None


@mcp.tool()
async def get_status() -> str:
    """Get olografix HQ status.

    Returns a string with the status of the Olografix HQ.
    """
    status = await make_sede_request(SEDE_API_STATUS)

    if not status:
        return "Unable to fetch status."

    return "open" if status == "true" else "closed"


def main():
    """Main entry point for the MCP server."""
    mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
