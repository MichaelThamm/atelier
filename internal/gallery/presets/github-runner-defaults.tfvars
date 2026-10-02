# The GitHub runner product module takes its image builder and its runner list
# as variables without defaults; every attribute of each is optional, so this
# takes the module's own defaults.
#
# One runner at the module's defaults, because the module requires a non-empty
# list. The image builder has to be deployed before that runner can register,
# so the user adds further runners after applying.

github_runner_image_builder = {}
github_runners              = [{ app_name = "github-runner" }]