import {ChangeDetectionStrategy, Component, inject, signal} from '@angular/core';
import {MatSidenavModule} from '@angular/material/sidenav';
import {MatExpansionModule} from '@angular/material/expansion';
import {MatListModule} from '@angular/material/list';
import {MatIconModule} from '@angular/material/icon';
import {NavigationEnd, Router, RouterLink} from '@angular/router';
import {MatButtonModule} from '@angular/material/button';
import {filter} from 'rxjs';
import {NgClass} from '@angular/common';
import {MatDialog} from '@angular/material/dialog';
import {UploadDialogComponent} from '../upload-dialog/upload-dialog.component';

@Component({
  selector: 'app-sidebar',
  imports: [MatSidenavModule, MatExpansionModule, MatListModule, MatIconModule, RouterLink, MatButtonModule, NgClass],
  templateUrl: './sidebar.component.html',
  styleUrl: './sidebar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppSidebarComponent {
  protected readonly currentRoute = signal<string>('');
  private readonly dialog = inject(MatDialog);

  constructor(private router: Router) {
    this.router.events
      .pipe(filter(event => event instanceof NavigationEnd))
      .subscribe((event: NavigationEnd) => {
        this.currentRoute.set(event.urlAfterRedirects);
      });
  }

  protected openUploadDialog(): void {
    this.dialog.open(UploadDialogComponent);
  }
}
