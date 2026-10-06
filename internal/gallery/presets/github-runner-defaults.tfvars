# One runner at the module's defaults, with everything else left to the module.

github_runner_image_builder = {}
github_runners              = [{ app_name = "github-runner" }]

# The image builder and the runner list have no defaults and the module rejects
# an empty list, so both are set here. The image builder has to be running before
# the runner registers, so the user adds further runners after applying.
