from urllib.parse import urlparse, parse_qs

def video_id(url):
    """Return the v query parameter, or an empty string when absent."""
    return parse_qs(urlparse(url).query).get("v", [""])[0]
