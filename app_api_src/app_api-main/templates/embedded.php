<?php

/**
 * SPDX-FileCopyrightText: 2024 Nextcloud GmbH and Nextcloud contributors
 * SPDX-License-Identifier: AGPL-3.0-or-later
 */
?>

<div id="content"></div>
<script nonce="<?php p(\OC::$server->getContentSecurityPolicyNonceManager()->getNonce()) ?>">
  console.log("Fetching proxy to check headers...");
  var url = window.OC.generateUrl('/apps/app_api/proxy/video_converter/ui/convert.html');
  fetch(url).then(res => {
    console.log("Status:", res.status);
    console.log("Headers:");
    res.headers.forEach((v, k) => console.log(k, v));
  }).catch(err => console.error("Fetch error:", err));
</script>
