from jadi import component

from aj.plugins.core.api.sidebar import SidebarItemProvider


@component(SidebarItemProvider)
class FrigotehnicaSidebarItem(SidebarItemProvider):
    def __init__(self, context):
        self.context = context

    def provide(self):
        return [{
            'attach': 'category:tools',
            'name': 'Cloudflare Tunnel',
            'icon': 'cloud',
            'url': '/view/frigotehnica-tunnel',
            'children': [],
        }]

