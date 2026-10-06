# Copyright 2026 Canonical Ltd.
# See LICENSE file for licensing details.
"""`--list-var-files` lists the bundles a module can use, not every bundled one.

The gallery spans every product Atelier ships, so an unfiltered listing named
ten presets for `cos-lite`, nine of which set inputs `cos-lite` does not
declare. These pin that the listing is narrowed to the module on the command
line, and that `--all` widens it back. Classic wrapper shape; runs in the fast
tier (no Juju model).
"""

from helpers import atelier


def test_list_var_files_narrows_bundled_presets_to_the_module(tmp_path):
    # WHEN the bundles for cos-lite are listed
    result = atelier("apply cos-lite --list-var-files", cwd=tmp_path)

    # THEN a bundled preset that deploys cos-lite is offered
    assert "cos-lite-no-ingress" in result.stdout, result.stdout + result.stderr
    # AND presets for other products are not, though Atelier bundles them too
    for unrelated in ("trino-defaults", "netbox-defaults", "spark-single-unit"):
        assert unrelated not in result.stdout, (
            f"{unrelated} should not be listed for cos-lite:\n{result.stdout}"
        )


def test_list_var_files_separates_subdirectories_of_one_repo(tmp_path):
    # GIVEN cos and cos-lite are two directories of one repository
    lite = atelier(
        "apply https://github.com/canonical/observability-stack.git"
        " --module terraform/cos-lite --list-var-files",
        cwd=tmp_path,
    )
    cos = atelier(
        "apply https://github.com/canonical/observability-stack.git"
        " --module terraform/cos --list-var-files",
        cwd=tmp_path,
    )

    # THEN each listing offers only the presets for its own subdirectory
    assert "cos-lite-no-ingress" in lite.stdout, lite.stdout
    assert "cos-lite-no-ingress" not in cos.stdout, cos.stdout
    assert "cos-no-ingress" in cos.stdout, cos.stdout


def test_list_var_files_all_widens_to_every_bundled_preset(tmp_path):
    # WHEN --all is passed alongside --list-var-files
    result = atelier("apply cos-lite --list-var-files --all", cwd=tmp_path)

    # THEN the bundles for other products come back
    for other in ("trino-defaults", "netbox-defaults"):
        assert other in result.stdout, f"--all should list {other}:\n{result.stdout}"


def test_all_without_list_var_files_is_refused(tmp_path):
    # WHEN --all is passed on its own
    result = atelier("apply cos-lite --all", cwd=tmp_path, check=False)

    # THEN it is refused rather than silently ignored
    assert result.returncode != 0, result.stdout + result.stderr
    assert "--all" in (result.stdout + result.stderr)
    # AND nothing was scaffolded
    assert not (tmp_path / "cos-lite").exists()
