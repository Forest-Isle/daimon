## ADDED Requirements

### Requirement: Fact-to-summary compaction
The system SHALL run a background compaction process that merges facts into structured summaries. When facts with the same `category` value in `user/` scope exceed a configurable threshold K (default=8), the compactor SHALL use the LLM to merge the oldest K facts into one summary file. The summary file SHALL have frontmatter: `type: summary`, `source_facts: [list of merged fact IDs]`, `category: <original category>`.

#### Scenario: Category threshold reached
- **WHEN** 9 facts with `category: preference` exist in `user/` scope
- **THEN** the compactor SHALL merge the 8 oldest into one summary file with `type: summary` and `source_facts` listing the 8 merged fact IDs

#### Scenario: Category below threshold
- **WHEN** 5 facts with `category: fact` exist in `user/` scope
- **THEN** the compactor SHALL NOT create a summary for that category

#### Scenario: Source facts preserved after compaction
- **WHEN** a summary is created from 8 source facts
- **THEN** the source facts SHALL NOT be deleted; they SHALL continue to exist and decay naturally via the forgetting curve

### Requirement: User profile generation
The system SHALL generate and maintain a user profile file at `user/profile_{user_id}.md` that synthesizes identity, preferences, and current focus from Level-1 reflections. The profile SHALL be regenerated after every 5 Level-1 reflections. The profile file SHALL have frontmatter: `type: profile`, `user_id: <user_id>`.

#### Scenario: Profile generation trigger
- **WHEN** 5 new Level-1 reflections have been generated since the last profile update
- **THEN** the system SHALL regenerate the user profile by providing all Level-1 and Level-2 reflections plus the existing profile (if any) to the LLM

#### Scenario: Profile structure
- **WHEN** a user profile is generated
- **THEN** the profile content SHALL contain sections for: Identity (who the user is), Preferences (how they like to work), and Current Focus (what they're working on)

#### Scenario: First profile generation
- **WHEN** 5 Level-1 reflections exist for a user and no profile file exists
- **THEN** the system SHALL create a new profile file at `user/profile_{user_id}.md`

### Requirement: Layered retrieval strategy
The system SHALL implement a layered retrieval strategy for memory search:
1. Always load the user profile file (if it exists) and include it in the system prompt as a "User Context" section
2. Search summaries first (filter by `type: summary`)
3. If summary results are fewer than the desired result count, backfill with raw facts (filter by `type != summary`)
4. Deduplicate: if a summary's `source_facts` overlap with a raw fact hit, prefer the summary

#### Scenario: Profile always loaded
- **WHEN** a system prompt is being built and a user profile exists
- **THEN** the profile content SHALL be included in the system prompt regardless of the search query

#### Scenario: Summary preferred over raw facts
- **WHEN** a search returns a summary whose `source_facts` include fact IDs that also appear in raw fact results
- **THEN** the overlapping raw facts SHALL be excluded from the final results and the summary SHALL be kept

#### Scenario: Backfill when summaries insufficient
- **WHEN** a search for "deployment" returns 2 summaries but the desired result count is 5
- **THEN** the system SHALL backfill with up to 3 raw fact results matching "deployment"

### Requirement: Compaction background task
The system SHALL run the compaction check as a background task with a configurable interval (default: 6 hours). The task SHALL scan all categories in `user/` scope and trigger compaction for any category exceeding the threshold.

#### Scenario: Background compaction cycle
- **WHEN** the compaction background task runs
- **THEN** it SHALL check every distinct category in `user/` scope and create summaries for any category exceeding the threshold K
