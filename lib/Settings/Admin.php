<?php

declare(strict_types=1);

namespace OCA\VideoConverterExApp\Settings;

use OCA\VideoConverterExApp\AppInfo\Application;
use OCP\AppFramework\Http\TemplateResponse;
use OCP\IConfig;
use OCP\IGroupManager;
use OCP\IURLGenerator;
use OCP\Settings\ISettings;

class Admin implements ISettings {
	public function __construct(
		private IConfig $config,
		private IGroupManager $groupManager,
		private IURLGenerator $urlGenerator,
	) {
	}

	public function getForm(): TemplateResponse {
		$groups = array_map(
			static fn ($group): array => [
				'id' => $group->getGID(),
				'name' => $group->getDisplayName(),
			],
			$this->groupManager->search('')
		);
		usort($groups, static fn (array $a, array $b): int => strcasecmp($a['name'], $b['name']));

		return new TemplateResponse(Application::APP_ID, 'admin', [
			'saveUrl' => $this->urlGenerator->linkToRoute(Application::APP_ID . '.settings.save'),
			'groups' => $groups,
			'values' => [
				'allowed_groups' => $this->getString('allowed_groups', ''),
				'max_concurrent_jobs' => $this->getInt('max_concurrent_jobs', 1),
				'max_concurrent_jobs_per_user' => $this->getInt('max_concurrent_jobs_per_user', 1),
				'max_queued_jobs_per_user' => $this->getInt('max_queued_jobs_per_user', 3),
				'job_timeout_minutes' => $this->getInt('job_timeout_minutes', 120),
				'cpu_limit_percent' => $this->getInt('cpu_limit_percent', 50),
				'threads_per_job' => $this->getInt('threads_per_job', 0),
			],
		], '');
	}

	public function getSection(): string {
		return 'declarative_settings';
	}

	public function getPriority(): int {
		return 50;
	}

	private function getString(string $key, string $default): string {
		return $this->config->getAppValue(Application::APP_ID, $key, $default);
	}

	private function getInt(string $key, int $default): int {
		return (int)$this->config->getAppValue(Application::APP_ID, $key, (string)$default);
	}
}
