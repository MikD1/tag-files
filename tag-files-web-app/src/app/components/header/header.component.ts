import { Component, inject, output } from '@angular/core';
import { MatToolbarModule } from '@angular/material/toolbar';
import { MatIconModule } from '@angular/material/icon';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { AppStateService } from '../../services/app-state.service';

@Component({
  selector: 'app-header',
  imports: [MatToolbarModule, MatIconModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  templateUrl: './header.component.html',
  styleUrl: './header.component.scss'
})
export class AppHeaderComponent {
  onAddClick() {
    this.appStateService.increaseMainGridColumns();
  }

  onRemoveClick() {
    this.appStateService.decreaseMainGridColumns();
  }

  toggleSidebar() {
    this.appStateService.toggleSidebar();
  }

  private readonly appStateService = inject(AppStateService);
}
