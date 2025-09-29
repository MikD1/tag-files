using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Storage.ValueConversion;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Infrastructure;

public sealed class AppDbContext(DbContextOptions<AppDbContext> options) : DbContext(options)
{
    public DbSet<Tag> Tags => Set<Tag>();

    public DbSet<LibraryItem> LibraryItems => Set<LibraryItem>();

    public DbSet<ProcessingFile> ProcessingFiles => Set<ProcessingFile>();

    public DbSet<Category> Categories => Set<Category>();

    public DbSet<LibraryCollection> Collections => Set<LibraryCollection>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        ValueConverter<TimeSpan?, long?> timeSpanTicksConverter = new(
            x => x.HasValue ? x.Value.Ticks : null,
            x => x.HasValue ? TimeSpan.FromTicks(x.Value) : null);

        modelBuilder.Entity<LibraryItem>()
            .Property(e => e.FileType).HasConversion<string>();
        modelBuilder.Entity<LibraryItem>()
            .Property(e => e.ThumbnailStatus).HasConversion<string>();
        modelBuilder.Entity<LibraryItem>()
            .Property(e => e.VideoDuration)
            .HasConversion(timeSpanTicksConverter);
        modelBuilder.Entity<LibraryItem>()
            .HasMany(e => e.Tags).WithMany();

        modelBuilder.Entity<Category>()
            .Property(e => e.ItemsType).HasConversion<string>();
    }
}