module.exports = async (request, response) => {
  const owner = process.env.RELEASE_REPO_OWNER || "axeprpr";
  const repo = process.env.RELEASE_REPO_NAME || "tiny-agent-cli";
  const ossMirrorBase = process.env.OSS_MIRROR_BASE || "";

  let release = null;
  try {
    const upstream = await fetch(`https://api.github.com/repos/${owner}/${repo}/releases/latest`, {
      headers: {
        Accept: "application/vnd.github+json",
        "User-Agent": "tacli-release-site",
      },
    });
    if (upstream.ok) {
      const data = await upstream.json();
      release = {
        tag_name: data.tag_name || "",
        published_at: data.published_at || "",
        html_url: data.html_url || `https://github.com/${owner}/${repo}/releases`,
      };
    }
  } catch (_error) {
    release = null;
  }

  response.setHeader("Cache-Control", "s-maxage=300, stale-while-revalidate=3600");
  response.status(200).json({
    owner,
    repo,
    ossMirrorBase,
    release,
  });
};
