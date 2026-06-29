(function() {
	'use strict';

	function ready(callback) {
		if (document.readyState === 'loading') {
			document.addEventListener('DOMContentLoaded', callback);
		} else {
			callback();
		}
	}

	ready(function() {
		var form = document.getElementById('video-converter-settings-form');
		var picker = document.getElementById('video-converter-group-picker');
		var addButton = document.getElementById('video-converter-add-group');
		var selectedList = document.getElementById('video-converter-selected-groups');
		var hiddenGroups = document.getElementById('video-converter-allowed-groups');
		var requestToken = document.getElementById('video-converter-requesttoken');

		if (!form || !picker || !addButton || !selectedList || !hiddenGroups) {
			return;
		}

		var selected = hiddenGroups.value
			.split(',')
			.map(function(value) { return value.trim(); })
			.filter(Boolean);

		function syncHidden() {
			hiddenGroups.value = selected.join(',');
		}

		function markOptions() {
			Array.prototype.forEach.call(picker.options, function(option) {
				if (!option.value) {
					return;
				}
				option.disabled = selected.indexOf(option.value) !== -1;
			});
		}

		function render() {
			selectedList.innerHTML = '';
			selected.forEach(function(groupId) {
				var item = document.createElement('li');
				item.dataset.groupId = groupId;

				var label = document.createElement('span');
				label.textContent = groupId;
				item.appendChild(label);

				var remove = document.createElement('button');
				remove.type = 'button';
				remove.className = 'button icon-delete';
				remove.setAttribute('aria-label', 'Remove ' + groupId);
				remove.addEventListener('click', function() {
					selected = selected.filter(function(value) { return value !== groupId; });
					syncHidden();
					render();
				});
				item.appendChild(remove);
				selectedList.appendChild(item);
			});
			markOptions();
		}

		addButton.addEventListener('click', function() {
			var value = picker.value;
			if (!value || selected.indexOf(value) !== -1) {
				return;
			}
			selected.push(value);
			selected.sort(function(a, b) { return a.localeCompare(b); });
			picker.value = '';
			syncHidden();
			render();
		});

		picker.addEventListener('change', function() {
			if (picker.value) {
				addButton.focus();
			}
		});

		form.addEventListener('submit', function() {
			syncHidden();
			if (requestToken && window.OC && window.OC.requestToken) {
				requestToken.value = window.OC.requestToken;
			}
		});

		render();
	});
})();
