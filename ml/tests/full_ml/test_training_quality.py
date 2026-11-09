import unittest

import numpy as np
import torch
from rangle_training.temporal import (
    initialize_parameters,
    motion_prior_loss,
    temporal_values,
)
from test_temporal import temporal_fixture


class QualityModelTests(unittest.TestCase):
    def initialize(self, profile="baseline"):
        data = temporal_fixture()
        return initialize_parameters(
            data["xyz"],
            data["rgb"],
            np.full(100, 0.01),
            data["frame_times"],
            "cpu",
            quality_profile=profile,
        )

    def test_default_baseline_preserves_parameters_and_rng(self):
        data = temporal_fixture()
        torch.manual_seed(0)
        params, _, rates = initialize_parameters(
            data["xyz"], data["rgb"], np.full(100, 0.01), data["frame_times"], "cpu"
        )
        after_default = torch.random.get_rng_state()
        torch.manual_seed(0)
        explicit, _, explicit_rates = self.initialize("baseline")
        torch.testing.assert_close(torch.random.get_rng_state(), after_default)
        self.assertEqual(rates, explicit_rates)
        for key in params:
            torch.testing.assert_close(params[key], explicit[key], rtol=0, atol=0)
        torch.testing.assert_close(params["centre_times"], torch.full((100,), 0.04))
        torch.testing.assert_close(params["log_sigmas"].exp(), torch.full((100,), 0.08))

    def test_local_lifetime_keeps_count_and_support_across_all_times(self):
        baseline, _, _ = self.initialize()
        local, _, _ = self.initialize("local-lifetime")
        self.assertEqual(len(local["means"]), len(baseline["means"]))
        for key in set(baseline) - {"centre_times", "log_sigmas"}:
            torch.testing.assert_close(local[key], baseline[key], rtol=0, atol=0)
        self.assertAlmostEqual(float(local["centre_times"].min()), 0)
        self.assertAlmostEqual(float(local["centre_times"].max()), 0.08)
        for time in np.linspace(0, 0.08, 20):
            self.assertGreater(
                float(temporal_values(local, float(time))[1].max()), 0.099
            )
        self.assertAlmostEqual(float(local["log_sigmas"].exp().mean()), 0.02)

    def test_motion_prior_penalizes_static_projected_velocity_only(self):
        params, _, _ = self.initialize("motion-prior")
        with torch.no_grad():
            params["velocities"].fill_(0.1)
        K = torch.tensor([[80.0, 0, 48], [0, 80, 32], [0, 0, 1]])
        static = motion_prior_loss(
            params, 0.04, torch.eye(4), K, torch.zeros(64, 96), 0.08
        )
        moving = motion_prior_loss(
            params, 0.04, torch.eye(4), K, torch.ones(64, 96), 0.08
        )
        self.assertGreater(float(static), 0)
        self.assertEqual(float(moving), 0)
        static.backward()
        self.assertGreater(float(params["velocities"].grad.abs().sum()), 0)
        self.assertIsNone(params["means"].grad)
        self.assertIsNone(params["centre_times"].grad)


if __name__ == "__main__":
    unittest.main()
