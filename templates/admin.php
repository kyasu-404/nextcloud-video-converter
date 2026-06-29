<?php

declare(strict_types=1);

\OCP\Util::addScript('video_converter_exapp', 'admin');
\OCP\Util::addStyle('video_converter_exapp', 'admin');

$values = $_['values'];
$selectedGroups = array_values(array_filter(array_map('trim', explode(',', $values['allowed_groups'] ?? ''))));

?>

<div class="video-converter-admin">
	<h2>Video Converter</h2>
	<p class="settings-hint">Administrative settings for the Video Converter ExApp.</p>

	<form method="post" action="<?php p($_['saveUrl']); ?>" id="video-converter-settings-form">
		<input type="hidden" name="requesttoken" id="video-converter-requesttoken" value="">
		<input type="hidden" name="allowed_groups" id="video-converter-allowed-groups" value="<?php p($values['allowed_groups']); ?>">

		<section class="video-converter-settings-section">
			<h3>Access</h3>
			<div class="video-converter-field">
				<label for="video-converter-group-picker">Allowed groups</label>
				<div class="video-converter-group-row">
					<select id="video-converter-group-picker">
						<option value="">Select a group</option>
						<?php foreach ($_['groups'] as $group): ?>
							<option value="<?php p($group['id']); ?>"><?php p($group['name']); ?> (<?php p($group['id']); ?>)</option>
						<?php endforeach; ?>
					</select>
					<button type="button" class="button" id="video-converter-add-group">Add</button>
				</div>
				<ul class="video-converter-selected-groups" id="video-converter-selected-groups">
					<?php foreach ($selectedGroups as $groupId): ?>
						<li data-group-id="<?php p($groupId); ?>">
							<span><?php p($groupId); ?></span>
							<button type="button" class="button icon-delete" aria-label="Remove <?php p($groupId); ?>"></button>
						</li>
					<?php endforeach; ?>
				</ul>
				<p class="settings-hint">Leave empty to allow all users.</p>
			</div>
		</section>

		<section class="video-converter-settings-section">
			<h3>Queue &amp; limits</h3>
			<div class="video-converter-grid">
				<label>
					<span>Max concurrent jobs</span>
					<input type="number" name="max_concurrent_jobs" min="1" max="100" value="<?php p($values['max_concurrent_jobs']); ?>">
				</label>
				<label>
					<span>Max concurrent jobs per user</span>
					<input type="number" name="max_concurrent_jobs_per_user" min="1" max="100" value="<?php p($values['max_concurrent_jobs_per_user']); ?>">
				</label>
				<label>
					<span>Max queued jobs per user</span>
					<input type="number" name="max_queued_jobs_per_user" min="0" max="1000" value="<?php p($values['max_queued_jobs_per_user']); ?>">
				</label>
				<label>
					<span>Job timeout (min)</span>
					<input type="number" name="job_timeout_minutes" min="1" max="10080" value="<?php p($values['job_timeout_minutes']); ?>">
				</label>
			</div>
		</section>

		<section class="video-converter-settings-section">
			<h3>FFmpeg performance</h3>
			<div class="video-converter-grid">
				<label>
					<span>CPU limit</span>
					<input type="number" name="cpu_limit_percent" min="1" max="100" value="<?php p($values['cpu_limit_percent']); ?>">
					<small>Percent of total container CPU capacity.</small>
				</label>
				<label>
					<span>Threads per job</span>
					<input type="number" name="threads_per_job" min="0" max="256" value="<?php p($values['threads_per_job']); ?>">
					<small>Use 0 to let FFmpeg choose threads without limiting CPU cores.</small>
				</label>
			</div>
		</section>

		<div class="video-converter-actions">
			<button type="submit" class="primary">Сохранить</button>
		</div>
	</form>
</div>
