import unittest

from youtube_live_detector import point_in_polygon, zone_for, zones


class ZoneClassificationTests(unittest.TestCase):
    def test_zone_membership_matches_displayed_polygons(self):
        cases = [
            ((0.10, 0.60), "no-zone"),
            ((0.50, 0.80), "crosswalk"),
            ((0.29, 0.90), "crosswalk"),
            ((0.99, 0.70), "public"),
            ((0.50, 0.50), "public"),
        ]
        for point, want in cases:
            with self.subTest(point=point):
                self.assertEqual(zone_for(*point), want)

    def test_boundary_counts_as_inside(self):
        restricted = next(zone for zone in zones() if zone["id"] == "no-zone")
        self.assertTrue(point_in_polygon(0.0, 0.48, restricted["points"]))
        self.assertEqual(zone_for(0.0, 0.48), "no-zone")

    def test_restricted_zone_wins_overlap(self):
        point = (0.10, 0.73)
        matching = [zone["id"] for zone in zones() if point_in_polygon(*point, zone["points"])]
        self.assertEqual(set(matching), {"crosswalk", "no-zone"})
        self.assertEqual(zone_for(*point), "no-zone")


if __name__ == "__main__":
    unittest.main()
