using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace TagFilesService.Infrastructure.Migrations
{
    /// <inheritdoc />
    public partial class FileMetadataThumbnailStatusAddConversion : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AlterColumn<string>(
                name: "ThumbnailStatus",
                table: "FilesMetadata",
                type: "TEXT",
                nullable: false,
                oldClrType: typeof(int),
                oldType: "INTEGER");

            migrationBuilder.Sql(@"
                UPDATE FilesMetadata
                SET ThumbnailStatus =
                    CASE ThumbnailStatus
                        WHEN 0 THEN 'NotGenerated'
                        WHEN 1 THEN 'Generated'
                        WHEN 2 THEN 'Failed'
                        ELSE 'NotGenerated'
                    END");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            throw new NotImplementedException();
        }
    }
}
