<?php

declare(strict_types=1);

namespace OCA\VideoConverterExApp\Controller;

use OCA\VideoConverterExApp\AppInfo\Application;
use OCP\AppFramework\Controller;
use OCP\AppFramework\Http\RedirectResponse;
use OCP\IConfig;
use OCP\IRequest;
use OCP\IURLGenerator;

class SettingsController extends Controller {
	public function __construct(
		IRequest $request,
		private IConfig $config,
		private IURLGenerator $urlGenerator,
	) {
		parent::__construct(Application::APP_ID, $request);
	}

	public function save(): RedirectResponse {
		$this->setString('allowed_groups', $this->normalizeGroups((string)$this->request->getParam('allowed_groups', '')));
		$this->setInt('max_concurrent_jobs', 1, 100, 1);
		$this->setInt('max_concurrent_jobs_per_user', 1, 100, 1);
		$this->setInt('max_queued_jobs_per_user', 0, 1000, 3);
		$this->setInt('job_timeout_minutes', 1, 10080, 120);
		$this->setInt('cpu_limit_percent', 1, 100, 50);
		$this->setInt('threads_per_job', 0, 256, 0);

		return new RedirectResponse($this->urlGenerator->linkToRoute('settings.AdminSettings.index', [
			'section' => 'declarative_settings',
		]));
	}

	private function setString(string $key, string $value): void {
		$this->config->setAppValue(Application::APP_ID, $key, $value);
	}

	private function setInt(string $key, int $min, int $max, int $default): void {
		$value = filter_var($this->request->getParam($key, $default), FILTER_VALIDATE_INT);
		if ($value === false) {
			$value = $default;
		}
		$value = max($min, min($max, (int)$value));
		$this->config->setAppValue(Application::APP_ID, $key, (string)$value);
	}

	private function normalizeGroups(string $value): string {
		$groups = array_filter(array_map('trim', explode(',', $value)), static fn (string $group): bool => $group !== '');
		$groups = array_values(array_unique($groups));
		return implode(',', $groups);
	}
}
