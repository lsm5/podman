PREFIX ?= /usr
BINDIR ?= $(PREFIX)/bin

.PHONY: rpm
rpm:
	$(shell $(BINDIR)/bash update-spec-version.sh)
	spectool -g podman.spec
	rpmbuild -ba \
		--define '_sourcedir $(shell pwd)' \
		--define '_rpmdir %{_sourcedir}/RPMS' \
		--define '_srcrpmdir %{_sourcedir}/SRPMS' \
		--define '_builddir %{_sourcedir}/BUILD' \
		$(shell pwd)/podman.spec
