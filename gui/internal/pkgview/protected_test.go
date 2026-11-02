package pkgview

import "testing"

// TestAdvisoryProtectedCoversTheWikiPromises (B4): without the helper's list
// the page still greys sudo, the kernels, the bootloader and archmaid.
func TestAdvisoryProtectedCoversTheWikiPromises(t *testing.T) {
	s := NewAdvisoryProtected()
	for _, n := range []string{"base", "pacman", "glibc", "sudo", "linux", "linux-lts", "linux-cachyos", "linux-cachyos-lts", "limine", "mkinitcpio", "archmaid", "archmaid-git", "archmaid-gui-git"} {
		if _, ok := s.Protected(n); !ok {
			t.Errorf("%s is not protected", n)
		}
	}
	for _, n := range []string{"htop", "linux-firmware", "pacman-contrib", "gvim"} {
		if _, ok := s.Protected(n); ok {
			t.Errorf("%s is protected", n)
		}
	}
}

// TestHelperListIsTheAuthority: the helper's names replace the advisory core
// (and its prefixes); a name a refusal taught stays.
func TestHelperListIsTheAuthority(t *testing.T) {
	s := NewAdvisoryProtected()
	s.Learn([]string{"nano"}, "Needed to boot.")
	s.UseHelperList([]string{"glibc", "sudo", "linux-cachyos"})
	for _, n := range []string{"glibc", "sudo", "linux-cachyos"} {
		if why, ok := s.Protected(n); !ok || why == "" {
			t.Errorf("%s: %q %v", n, why, ok)
		}
	}
	if why, ok := s.Protected("nano"); !ok || why != "Needed to boot." {
		t.Errorf("the learned name was lost: %q %v", why, ok)
	}
	if _, ok := s.Protected("linux-cachyos-headers"); ok {
		t.Error("an advisory prefix outlived the helper's list")
	}
	if _, ok := s.Protected("bash"); ok {
		t.Error("an advisory name outlived the helper's list")
	}
}
