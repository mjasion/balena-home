# EdgarTools MCP server (SEC EDGAR filings) over Streamable HTTP.
# Reached only through nginx → Cloudflare tunnel → Cloudflare Access (Managed OAuth);
# the server itself has no authentication.
FROM python:3.15.2-slim

# SEC requires a User-Agent with contact details ("Name email")
ENV EDGAR_IDENTITY="Marcin Jasion marcinjasion@gmail.com" \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt

RUN useradd --create-home edgar
USER edgar

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/health', timeout=4)" || exit 1

CMD ["edgartools-mcp", "--transport", "streamable-http", "--host", "0.0.0.0", "--port", "8000"]
