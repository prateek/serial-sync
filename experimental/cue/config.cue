package serialsync

#NonEmpty: string & =~"^.+$"
#ID:       #NonEmpty & =~"^[a-z0-9][a-z0-9-_]*$"

#Config: {
	runtime: {
		log_level:     *"info" | "debug" | "warn" | "error"
		log_format:    *"text" | "json"
		store_driver:  "sqlite"
		store_dsn:     #NonEmpty
		artifact_root: #NonEmpty
		support_root:  #NonEmpty
	}

	scheduler?: {
		mode:          *"manual" | "daemon"
		poll_interval: #NonEmpty
	}

	auth_profiles: *[] | [...{
		id:           #ID
		provider:     "patreon"
		mode:         "fixture" | "session" | "credentials"
		username_env?: #NonEmpty
		password_env?: #NonEmpty
		session_path?: #NonEmpty
	}]

	publishers: *[] | [...{
		id:      #ID
		kind:    "filesystem" | "command"
		enabled: *true | false
		path?:   #NonEmpty
		command?: [...#NonEmpty]

		if kind == "filesystem" {
			path: #NonEmpty
		}
		if kind == "command" {
			command: [...#NonEmpty] & [_, ...]
		}
	}]

	sources: [...{
		id:           #ID
		provider:     "patreon"
		url:          =~"^https?://"
		auth_profile?: #ID
		enabled:      *true | false
		fixture_dir?: #NonEmpty
	}]

	rules: *[] | [...{
		source:              #ID
		priority:            *100 | int
		match_type:          "tag" | "title_regex" | "title_contains"
		match_value:         #NonEmpty
		track_key:           #ID
		track_name:          #NonEmpty
		release_role:        "chapter" | "bonus" | "collection" | "full_release"
		content_strategy:    "attachment_preferred" | "text_post"
		attachment_glob?:    [...#NonEmpty]
		attachment_priority?: [...#NonEmpty]
		anthology_mode:      *false | true
	}]

	_authProfilesByID: {
		for profile in auth_profiles {
			"\(profile.id)": profile
		}
	}

	_sourceIDs: {
		for source in sources {
			"\(source.id)": true
		}
	}

	_authProfileChecks: [
		for source in sources
		if source.auth_profile != _|_ && source.auth_profile != "" {
			_authProfilesByID[source.auth_profile]
		}
	]

	_ruleSourceChecks: [for rule in rules { _sourceIDs[rule.source] }]
}
