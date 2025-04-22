import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { AsyncPipe } from '@angular/common';
import { MatSidenavModule } from '@angular/material/sidenav';
import { LibraryService } from '../../services/library.service';
import { Observable } from 'rxjs';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatListModule } from '@angular/material/list';
import { MatIconModule } from '@angular/material/icon';

@Component({
  selector: 'app-sidebar',
  imports: [MatSidenavModule, AsyncPipe, MatExpansionModule, MatListModule, MatIconModule],
  templateUrl: './sidebar.component.html',
  styleUrl: './sidebar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppSidebarComponent {
  constructor() {
    this.tags = this.libraryService.getTags();
  }

  protected tags: Observable<string[]>;

  private readonly libraryService = inject(LibraryService);
}
