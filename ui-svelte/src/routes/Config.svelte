<script lang="ts">
  import { onMount } from "svelte";
  import { fetchConfig, saveConfig } from "../stores/api";
  import { isDarkMode, isNarrow } from "../stores/theme";
  import ResizablePanels from "../components/ResizablePanels.svelte";
  import { EXAMPLE_CONFIG, CLI_ARGS_REFERENCE } from "../lib/ConfigConstants";

  let configContent = $state("");
  let configPath = $state("");
  let isLoading = $state(true);
  let error = $state("");
  let isSaving = $state(false);
  let hasChanges = $state(false);
  let validationError = $state("");
  let cliArgsSearch = $state("");
  let copiedArg = $state("");

  let editorContainer: HTMLDivElement;
  let exampleEditorContainer: HTMLDivElement;
  let editor: any;
  let exampleEditor: any;

  async function loadConfig() {
    isLoading = true;
    error = "";
    try {
      const data = await fetchConfig();
      configContent = data.content;
      configPath = data.path;
      hasChanges = false;
      if (editor) {
        editor.setValue(configContent);
      }
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to load config";
    } finally {
      isLoading = false;
    }
  }

  function validateYAML(content: string): string {
    try {
      // Basic YAML validation - check for common syntax errors
      const lines = content.split('\n');
      let indentStack: number[] = [0];

      for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        if (line.trim().startsWith('#') || line.trim() === '') continue;

        const indent = line.search(/\S/);
        if (indent === -1) continue;

        // Check for tabs (YAML doesn't allow tabs for indentation)
        if (line.match(/^\t/)) {
          return `Line ${i + 1}: Tabs are not allowed for indentation in YAML`;
        }
      }
      return "";
    } catch (err) {
      return err instanceof Error ? err.message : "Invalid YAML";
    }
  }

  function handleEditorChange(value: string) {
    configContent = value;
    hasChanges = true;
    validationError = validateYAML(value);
  }

  async function handleSave() {
    if (validationError) {
      alert(`Invalid YAML: ${validationError}`);
      return;
    }

    if (!confirm("Are you sure you want to save the configuration? This will reload llama-swap.")) {
      return;
    }

    isSaving = true;
    error = "";
    try {
      await saveConfig(configContent);
      hasChanges = false;
      alert("Configuration saved successfully! The server is reloading...");
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to save config";
      alert(`Failed to save: ${err instanceof Error ? err.message : "Unknown error"}`);
    } finally {
      isSaving = false;
    }
  }

  function handleExport() {
    const blob = new Blob([configContent], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "config.yaml";
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  function handleImport() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".yaml,.yml";
    input.onchange = (e) => {
      const file = (e.target as HTMLInputElement).files?.[0];
      if (!file) return;

      const reader = new FileReader();
      reader.onload = (e) => {
        const content = e.target?.result as string;
        if (content) {
          configContent = content;
          hasChanges = true;
          if (editor) {
            editor.setValue(content);
          }
          validationError = validateYAML(content);
        }
      };
      reader.readAsText(file);
    };
    input.click();
  }

  async function handleCopyArg(arg: string) {
    try {
      await navigator.clipboard.writeText(arg);
      copiedArg = arg;
      setTimeout(() => (copiedArg = ""), 2000);
    } catch (err) {
      console.error("Failed to copy:", err);
    }
  }

  function renderCliArgs(): string {
    const textToRender = cliArgsSearch.trim()
      ? filterCliArgs(CLI_ARGS_REFERENCE, cliArgsSearch)
      : CLI_ARGS_REFERENCE;

    if (textToRender === 'No matches found.') {
      return textToRender;
    }

    return textToRender;
  }

  function filterCliArgs(text: string, search: string): string {
    if (!search.trim()) return text;

    const searchLower = search.toLowerCase();
    const lines = text.split('\n');
    const filteredLines: string[] = [];

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.toLowerCase().includes(searchLower)) {
        if (i > 0 && !filteredLines.includes(lines[i - 1])) {
          filteredLines.push(lines[i - 1]);
        }
        filteredLines.push(line);
        if (i < lines.length - 1 && !filteredLines.includes(lines[i + 1])) {
          filteredLines.push(lines[i + 1]);
        }
      }
    }

    return filteredLines.length > 0 ? filteredLines.join('\n') : 'No matches found.';
  }

  onMount(async () => {
    // Dynamically import Monaco editor
    try {
      const monaco = await import('monaco-editor');

      // Configure Monaco
      monaco.editor.defineTheme('vs-dark-custom', {
        base: 'vs-dark',
        inherit: true,
        rules: [],
        colors: {}
      });

      // Create main editor
      editor = monaco.editor.create(editorContainer, {
        value: configContent,
        language: 'yaml',
        theme: $isDarkMode ? 'vs-dark' : 'vs',
        minimap: { enabled: true },
        scrollBeyondLastLine: false,
        fontSize: 14,
        wordWrap: 'on',
        automaticLayout: true,
      });

      editor.onDidChangeModelContent(() => {
        handleEditorChange(editor.getValue());
      });

      // Create example editor
      exampleEditor = monaco.editor.create(exampleEditorContainer, {
        value: EXAMPLE_CONFIG,
        language: 'yaml',
        theme: $isDarkMode ? 'vs-dark' : 'vs',
        readOnly: true,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        fontSize: 12,
        wordWrap: 'on',
        automaticLayout: true,
        lineNumbers: 'on',
      });

      // Update theme when it changes
      const unsubscribe = isDarkMode.subscribe(dark => {
        if (editor) editor.updateOptions({ theme: dark ? 'vs-dark' : 'vs' });
        if (exampleEditor) exampleEditor.updateOptions({ theme: dark ? 'vs-dark' : 'vs' });
      });

      return () => {
        unsubscribe();
        editor?.dispose();
        exampleEditor?.dispose();
      };
    } catch (err) {
      console.error('Failed to load Monaco editor:', err);
      error = 'Failed to load code editor. Using fallback.';
    }

    await loadConfig();
  });
</script>

{#if isLoading}
  <div class="flex items-center justify-center h-full">
    <p class="text-gray-600 dark:text-gray-400">Loading configuration...</p>
  </div>
{:else}
  <div class="flex flex-col h-full">
    <div class="flex items-center justify-between mb-2 px-2 {$isNarrow ? 'flex-col gap-2' : ''}">
      <div class="flex items-center gap-4 {$isNarrow ? 'flex-col items-start w-full' : ''}">
        <h2 class="text-lg font-semibold">Configuration Editor</h2>
        <span class="text-sm text-gray-600 dark:text-gray-400">{configPath}</span>
        {#if hasChanges}
          <span class="text-sm text-orange-600 dark:text-orange-400">● Unsaved changes</span>
        {/if}
      </div>
      <div class="flex gap-2">
        <button
          onclick={handleImport}
          class="px-3 py-2 bg-gray-600 text-white rounded hover:bg-gray-700 text-sm"
          title="Import config file"
        >
          Import
        </button>
        <button
          onclick={handleExport}
          class="px-3 py-2 bg-gray-600 text-white rounded hover:bg-gray-700 text-sm"
          title="Export current config"
        >
          Export
        </button>
        <button
          onclick={handleSave}
          disabled={isSaving || !hasChanges || !!validationError}
          class="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:bg-gray-400 disabled:cursor-not-allowed"
        >
          {isSaving ? "Saving..." : "Save Config"}
        </button>
      </div>
    </div>

    {#if error}
      <div class="mb-2 px-2 py-2 bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400 rounded">
        Error: {error}
      </div>
    {/if}

    {#if validationError}
      <div class="mb-2 px-2 py-2 bg-orange-100 dark:bg-orange-900/30 text-orange-700 dark:text-orange-400 rounded text-sm">
        YAML Validation: {validationError}
      </div>
    {/if}

    <ResizablePanels
      direction={$isNarrow ? "vertical" : "horizontal"}
      panel1Size={50}
      panel2Size={50}
    >
      {#snippet panel1()}
        <div class="h-full border border-gray-300 dark:border-gray-700 rounded overflow-hidden">
          <div bind:this={editorContainer} class="h-full w-full"></div>
        </div>
      {/snippet}

      {#snippet panel2()}
        <ResizablePanels direction="vertical" panel1Size={60} panel2Size={40}>
          {#snippet panel1()}
            <div class="h-full border border-gray-300 dark:border-gray-700 rounded flex flex-col">
              <div class="px-3 py-2 bg-gray-100 dark:bg-gray-800 border-b border-gray-300 dark:border-gray-700">
                <h3 class="text-sm font-semibold">config.example.yaml</h3>
              </div>
              <div class="flex-1 overflow-hidden">
                <div bind:this={exampleEditorContainer} class="h-full w-full"></div>
              </div>
            </div>
          {/snippet}

          {#snippet panel2()}
            <div class="h-full border border-gray-300 dark:border-gray-700 rounded flex flex-col">
              <div class="px-3 py-2 bg-gray-100 dark:bg-gray-800 border-b border-gray-300 dark:border-gray-700 space-y-2">
                <div class="flex items-center justify-between">
                  <h3 class="text-sm font-semibold">llama-server CLI Arguments</h3>
                  <span class="text-xs text-gray-500 dark:text-gray-400 italic">Click args to copy</span>
                </div>
                <input
                  type="text"
                  placeholder="Search arguments..."
                  bind:value={cliArgsSearch}
                  class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-gray-700 rounded bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div class="flex-1 overflow-auto p-4">
                <div class="text-xs whitespace-pre-wrap font-mono">
                  {#each renderCliArgs().split('\n') as line, i}
                    <div>
                      {#each line.match(/(-{1,2}[a-z0-9][-a-z0-9]*)/gi) || [] as arg}
                        {@const beforeArg = line.substring(0, line.indexOf(arg))}
                        {@const afterArg = line.substring(line.indexOf(arg) + arg.length)}
                        {beforeArg}
                        <button
                          onclick={() => handleCopyArg(arg)}
                          class="text-blue-600 dark:text-blue-400 hover:underline hover:bg-blue-100 dark:hover:bg-blue-900/30 px-0.5 rounded cursor-pointer transition-colors"
                          title="Click to copy '{arg}'"
                        >
                          {#if copiedArg === arg}
                            <span class="bg-green-200 dark:bg-green-800 px-1 rounded">{arg}</span>
                          {:else}
                            {arg}
                          {/if}
                        </button>
                        {afterArg}
                      {:else}
                        {line}
                      {/each}
                    </div>
                  {/each}
                </div>
              </div>
            </div>
          {/snippet}
        </ResizablePanels>
      {/snippet}
    </ResizablePanels>
  </div>
{/if}
