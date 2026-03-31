## ADDED Requirements

### Requirement: Reflection trigger via hybrid strategy
The system SHALL trigger a reflection when either condition is met: (a) the count of unreflected facts since the last reflection reaches N=10, or (b) topic drift is detected (cosine similarity between the running topic embedding and the last reflection's topic embedding drops below 0.7). Both thresholds SHALL be configurable.

#### Scenario: Count-based trigger
- **WHEN** 10 new facts have been processed by the lifecycle manager since the last reflection
- **THEN** the system SHALL trigger a reflection over those 10 facts

#### Scenario: Topic drift trigger
- **WHEN** 5 new facts have been processed and the cosine similarity between the current topic embedding and the last reflection topic embedding is 0.65
- **THEN** the system SHALL trigger a reflection over those 5 facts because drift threshold (0.7) is exceeded

#### Scenario: Neither condition met
- **WHEN** 3 new facts have been processed and the cosine similarity is 0.85
- **THEN** the system SHALL NOT trigger a reflection

### Requirement: Running topic embedding computation
The system SHALL maintain a running topic embedding as an exponential moving average of the embeddings of recently processed facts. The system SHALL use the existing CachedEmbedder for embedding computation, adding zero additional LLM embedding calls for cached content.

#### Scenario: Topic embedding update
- **WHEN** a new fact is processed by the lifecycle manager
- **THEN** the running topic embedding SHALL be updated as: `new_topic = α × fact_embedding + (1-α) × old_topic` where α=0.3

#### Scenario: First fact after reflection
- **WHEN** a fact is the first processed after a reflection (or system start)
- **THEN** the running topic embedding SHALL be initialized to that fact's embedding

### Requirement: Level-1 reflection generation
The system SHALL generate Level-1 reflections by providing the LLM with the batch of unreflected facts and asking it to identify patterns, themes, and synthesized insights. The output SHALL be stored as a memory file with frontmatter fields: `type: reflection`, `level: 1`, `source_facts: [list of fact IDs]`.

#### Scenario: Successful Level-1 reflection
- **WHEN** a reflection is triggered with 10 facts about Kubernetes operations
- **THEN** the system SHALL produce a reflection memory identifying patterns (e.g., "User is managing a Kubernetes cluster with focus on resource optimization and monitoring")

#### Scenario: Reflection file format
- **WHEN** a Level-1 reflection is generated
- **THEN** the resulting file SHALL be stored in the user's memory scope with frontmatter containing `type: reflection`, `level: 1`, and `source_facts` listing all input fact IDs

### Requirement: Level-2 meta-reflection generation
The system SHALL generate Level-2 reflections after every 5 Level-1 reflections for the same user. Level-2 reflections synthesize across Level-1 outputs to produce strategic insights. The output SHALL have frontmatter: `type: reflection`, `level: 2`, `source_reflections: [list of L1 reflection IDs]`.

#### Scenario: Level-2 trigger
- **WHEN** 5 Level-1 reflections exist for a user since the last Level-2 reflection
- **THEN** the system SHALL generate a Level-2 reflection synthesizing those 5 Level-1 outputs

#### Scenario: Insufficient Level-1 reflections
- **WHEN** only 3 Level-1 reflections exist since the last Level-2 reflection
- **THEN** the system SHALL NOT generate a Level-2 reflection

### Requirement: Reflection tracker state persistence
The system SHALL persist the reflection tracker state (unreflected fact count, running topic embedding, last reflection topic embedding, Level-1 count since last Level-2) across agent restarts. State SHALL be stored in the memory index SQLite database.

#### Scenario: Agent restart preserves state
- **WHEN** the agent restarts after processing 7 unreflected facts
- **THEN** the reflection tracker SHALL resume with count=7 and continue accumulating toward the trigger threshold
