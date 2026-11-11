// Keep settings (including the Notion token) in trusted extension contexts.
chrome.runtime.onInstalled.addListener(() => {
  void chrome.storage.local.setAccessLevel({ accessLevel: "TRUSTED_CONTEXTS" });
});
